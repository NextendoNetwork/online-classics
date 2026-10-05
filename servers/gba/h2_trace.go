package main

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
)

// Observes frame metadata only. Does not decode HPACK or log payloads.
// Constant memory even if the peer declares a very large frame.
type h2FrameTrace struct {
	logger    *log.Logger
	id        uint64
	direction string
	preface   int
	header    [9]byte
	headerN   int
	remaining int
	prefix    [8]byte
	prefixN   int
	disabled  bool
}

const h2Preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

func (t *h2FrameTrace) feed(p []byte) {
	if t.disabled {
		return
	}
	for len(p) > 0 {
		if t.preface < len(h2Preface) {
			if p[0] != h2Preface[t.preface] {
				t.logger.Printf("h2: connection=%d direction=%s invalid_preface position=%d", t.id, t.direction, t.preface)
				t.disabled = true
				return
			}
			t.preface++
			p = p[1:]
			if t.preface == len(h2Preface) {
				t.logger.Printf("h2: connection=%d client_preface=complete", t.id)
			}
			continue
		}
		if t.headerN < 9 {
			n := copy(t.header[t.headerN:], p)
			t.headerN += n
			p = p[n:]
			if t.headerN < 9 {
				continue
			}
			t.remaining = int(t.header[0])<<16 | int(t.header[1])<<8 | int(t.header[2])
			t.logger.Printf("h2: connection=%d direction=%s type=%d flags=0x%02x stream=%d length=%d", t.id, t.direction, t.header[3], t.header[4], binary.BigEndian.Uint32(t.header[5:])&0x7fffffff, t.remaining)
			if t.remaining == 0 {
				t.headerN = 0
				continue
			}
		}
		n := min(len(p), t.remaining)
		// Retain only numeric RST_STREAM and GOAWAY fields.
		if t.header[3] == 3 || t.header[3] == 7 {
			t.prefixN += copy(t.prefix[t.prefixN:], p[:n])
		}
		p = p[n:]
		t.remaining -= n
		if t.remaining == 0 {
			if t.header[3] == 3 && t.prefixN >= 4 {
				t.logger.Printf("h2: connection=%d direction=%s RST_STREAM code=%d", t.id, t.direction, binary.BigEndian.Uint32(t.prefix[:4]))
			}
			if t.header[3] == 7 && t.prefixN == 8 {
				t.logger.Printf("h2: connection=%d direction=%s GOAWAY last_stream=%d code=%d", t.id, t.direction, binary.BigEndian.Uint32(t.prefix[:4])&0x7fffffff, binary.BigEndian.Uint32(t.prefix[4:]))
			}
			t.headerN = 0
			t.prefixN = 0
		}
	}
}

type tracedTLSConn struct {
	*tls.Conn
	in, out         h2FrameTrace
	readMu, writeMu sync.Mutex
	closeOnce       sync.Once
}

// HandshakeComplete does not establish subsequent certificate validation by
// the remote application. Logs negotiated metadata and errors exposed by
// crypto/tls; close_notify and TCP EOF cannot be distinguished here.
func (c *tracedTLSConn) HandshakeContext(ctx context.Context) error {
	err := c.Conn.HandshakeContext(ctx)
	s := c.Conn.ConnectionState()
	c.in.logger.Printf("tls: connection=%d handshake_complete=%t sni=%q version=0x%04x alpn=%q result=%q", c.in.id, s.HandshakeComplete, s.ServerName, s.Version, s.NegotiatedProtocol, tlsErrorSummary(err))
	return err
}

func tlsErrorSummary(err error) string {
	if err == nil {
		return "ok"
	}
	if errors.Is(err, io.EOF) {
		return "EOF_or_close_notify"
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return "unexpected_EOF"
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "remote error" && strings.HasPrefix(op.Err.Error(), "tls: ") {
		// Fixed crypto/tls alert text, not client data.
		return "remote_alert: " + op.Err.Error()
	}
	var alert tls.AlertError
	if errors.As(err, &alert) {
		return alert.Error()
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return "timeout"
	}
	var record tls.RecordHeaderError
	if errors.As(err, &record) {
		return "invalid_TLS_header"
	}
	return "transport_or_TLS_error"
}

func (c *tracedTLSConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	n, err := c.Conn.Read(p)
	if c.ConnectionState().NegotiatedProtocol == "h2" {
		c.in.feed(p[:n])
	}
	if err != nil {
		c.in.logger.Printf("h2: connection=%d read_finished=true eof=%t reason=%q", c.in.id, errors.Is(err, io.EOF), tlsErrorSummary(err))
	}
	return n, err
}

func (c *tracedTLSConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	n, err := c.Conn.Write(p)
	if c.ConnectionState().NegotiatedProtocol == "h2" {
		c.out.feed(p[:n])
	}
	return n, err
}

func (c *tracedTLSConn) Close() error {
	c.closeOnce.Do(func() { c.in.logger.Printf("h2: connection=%d local_close=true", c.in.id) })
	return c.Conn.Close()
}

type tracedTLSListener struct {
	net.Listener
	config *tls.Config
	logger *log.Logger
	next   atomic.Uint64
}

func (l *tracedTLSListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	id := l.next.Add(1)
	l.logger.Printf("h2: connection=%d accepted=true", id)
	return &tracedTLSConn{Conn: tls.Server(c, l.config),
		in:  h2FrameTrace{logger: l.logger, id: id, direction: "client->server"},
		out: h2FrameTrace{logger: l.logger, id: id, direction: "server->client", preface: len(h2Preface)}}, nil
}
