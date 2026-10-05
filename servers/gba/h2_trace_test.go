package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type traceBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *traceBuffer) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(p)
}
func (b *traceBuffer) text() string { b.Lock(); defer b.Unlock(); return b.Buffer.String() }

func TestH2TraceFragmentedAndPrivate(t *testing.T) {
	var buf bytes.Buffer
	x := h2FrameTrace{logger: log.New(&buf, "", 0), direction: "client->server"}
	data := append([]byte(h2Preface), []byte{0, 0, 0, 4, 0, 0, 0, 0, 0}...)
	secret := []byte("authorization-secret")
	data = append(data, []byte{0, 0, byte(len(secret)), 1, 4, 0, 0, 0, 1}...)
	data = append(data, secret...)
	data = append(data, []byte{0, 0, 4, 3, 0, 0, 0, 0, 1, 0, 0, 0, 8}...)
	data = append(data, []byte{0, 0, 8, 7, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 2}...)
	for _, b := range data {
		x.feed([]byte{b})
	}
	for _, want := range []string{"client_preface=complete", "type=4", "RST_STREAM code=8", "GOAWAY last_stream=1 code=2"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("Missing %s: %s", want, buf.String())
		}
	}
	if strings.Contains(buf.String(), string(secret)) {
		t.Fatal("Content leaked into logs")
	}
}

func TestH2TraceTLSRoundTrip(t *testing.T) {
	cert, err := developmentCertificate()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var buf traceBuffer
	logger := log.New(&buf, "", 0)
	p := new(http.Protocols)
	p.SetHTTP2(true)
	srv := &http.Server{Protocols: p, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.TLS == nil || r.Header.Get("Authorization") != "secret-test-token" {
			t.Error("TLS/H2 request changed")
		}
		payload, _ := io.ReadAll(r.Body)
		w.Write(payload)
	})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Serve(&tracedTLSListener{Listener: ln, config: &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2"}}, logger: logger})
	}()
	defer func() { srv.Close(); <-done }()
	tr := &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{InsecureSkipVerify: true, ServerName: "localhost"}} // Local test certificate.
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	req, _ := http.NewRequest("POST", "https://"+ln.Addr().String(), strings.NewReader("private-body"))
	req.Header.Set("Authorization", "secret-test-token")
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || string(b) != "private-body" || res.ProtoMajor != 2 {
		t.Fatalf("Response changed %q %v", b, err)
	}
	output := buf.text()
	if !strings.Contains(output, "client_preface=complete") || !strings.Contains(output, "type=1") {
		t.Fatalf("Missing frames: %s", output)
	}
	if !strings.Contains(output, `sni="localhost"`) || !strings.Contains(output, `result="ok"`) {
		t.Fatal("Missing TLS handshake data")
	}
	if strings.Contains(output, "secret-test-token") || strings.Contains(output, "private-body") {
		t.Fatal("Private data in logs")
	}
}

func TestTLSRejectedCertificateReportsRemoteAlert(t *testing.T) {
	cert, err := developmentCertificate()
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var buf traceBuffer
	l := &tracedTLSListener{Listener: ln, config: &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2"}}, logger: log.New(&buf, "", 0)}
	done := make(chan error, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		done <- conn.(*tracedTLSConn).HandshakeContext(ctx)
	}()
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", ln.Addr().String(), &tls.Config{ServerName: "localhost", NextProtos: []string{"h2"}})
	if err == nil {
		conn.Close()
		t.Fatal("Ephemeral certificate must not be trusted")
	}
	select {
	case serverErr := <-done:
		if serverErr == nil || !strings.Contains(tlsErrorSummary(serverErr), "remote_alert") {
			t.Fatalf("TLS alert not recognized: %v", serverErr)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("Rejected handshake did not finish")
	}
	if !strings.Contains(buf.text(), `sni="localhost"`) || !strings.Contains(buf.text(), "handshake_complete=false") {
		t.Fatal("Missing TLS rejection diagnostic")
	}
}
