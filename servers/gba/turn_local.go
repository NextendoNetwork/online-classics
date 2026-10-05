package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v5"
)

const localTURNRealm = "genesis-lab"

type localTURN struct {
	server   *turn.Server
	endpoint iceEndpoint
	secret   [32]byte
}

func (t *localTURN) Close() error { return t.server.Close() }

func (t *localTURN) credentials(now time.Time) (username, password string, err error) {
	var nonce [12]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return
	}
	username = fmt.Sprintf("%d:%s", now.Add(time.Hour).Unix(), base64.RawURLEncoding.EncodeToString(nonce[:]))
	password = t.password(username)
	return
}

// coturn-compatible TURN REST credentials: HMAC-SHA1 and a timestamped username.
// The secret never leaves the process; usernames/passwords are never logged.
func (t *localTURN) password(username string) string {
	mac := hmac.New(sha1.New, t.secret[:])
	mac.Write([]byte(username))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func validTURNUsername(username string, now time.Time) bool {
	parts := strings.Split(username, ":")
	if len(parts) != 2 {
		return false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || expiry <= now.Unix() || expiry > now.Add(time.Hour+time.Minute).Unix() {
		return false
	}
	nonce, err := base64.RawURLEncoding.DecodeString(parts[1])
	return err == nil && len(nonce) == 12
}

type observedTURNConn struct {
	net.PacketConn
	logger *log.Logger
	reads  atomic.Uint32
	writes atomic.Uint32
}

func (c *observedTURNConn) WriteTo(buffer []byte, addr net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(buffer, addr)
	if c.writes.Add(1) <= 32 {
		typ, code := uint16(0), 0
		if len(buffer) >= 20 {
			typ = binary.BigEndian.Uint16(buffer[:2])
			for offset := 20; offset+4 <= len(buffer); {
				attribute := binary.BigEndian.Uint16(buffer[offset : offset+2])
				length := int(binary.BigEndian.Uint16(buffer[offset+2 : offset+4]))
				if offset+4+length > len(buffer) {
					break
				}
				if attribute == 9 && length >= 4 {
					code = int(buffer[offset+6]&7)*100 + int(buffer[offset+7])
				}
				offset += 4 + (length+3)&^3
			}
		}
		c.logger.Printf("TURN local: reply bytes=%d sent=%d type=0x%04x error_code=%d send_ok=%t", len(buffer), n, typ, code, err == nil)
	}
	return n, err
}

func (c *observedTURNConn) ReadFrom(buffer []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(buffer)
	if err == nil && c.reads.Add(1) <= 16 {
		typ := uint16(0)
		if n >= 2 {
			typ = binary.BigEndian.Uint16(buffer[:2])
		}
		c.logger.Printf("TURN local: datagram received bytes=%d type=0x%04x", n, typ)
	}
	return n, addr, err
}

func startLocalTURN(address string, logger *log.Logger) (*localTURN, error) {
	addr, err := net.ResolveUDPAddr("udp4", address)
	if err != nil {
		return nil, err
	}
	if addr.IP == nil || addr.IP.To4() == nil {
		return nil, errors.New("turn-addr requires IPv4")
	}
	subnet, err := labListenerNetwork(addr.IP)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return nil, err
	}
	bound := conn.LocalAddr().(*net.UDPAddr)
	t := &localTURN{endpoint: iceEndpoint{bound.IP.String(), bound.Port}}
	if _, err = rand.Read(t.secret[:]); err != nil {
		conn.Close()
		return nil, err
	}
	// Pion debug logs may expose identities/data: discard that logger.
	factory := logging.NewDefaultLoggerFactory()
	factory.Writer = io.Discard
	var authAttempts atomic.Uint32
	t.server, err = turn.NewServer(turn.ServerConfig{
		Realm:         localTURNRealm,
		LoggerFactory: factory,
		AuthHandler: func(request *turn.RequestAttributes) (string, []byte, bool) {
			source, ok := request.SrcAddr.(*net.UDPAddr)
			sourceOK := ok && labPeerAllowed(subnet, source.IP)
			realmOK := request.Realm == localTURNRealm
			usernameOK := validTURNUsername(request.Username, time.Now())
			if authAttempts.Add(1) <= 16 {
				logger.Printf("TURN local: authentication source_allowed=%t realm_valid=%t username_format_and_date_valid=%t", sourceOK, realmOK, usernameOK)
			}
			if !sourceOK || !realmOK || !usernameOK {
				return "", nil, false
			}
			return request.Username, turn.GenerateAuthKey(request.Username, localTURNRealm, t.password(request.Username)), true
		},
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn:            &observedTURNConn{PacketConn: conn, logger: logger},
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: bound.IP, Address: bound.IP.String()},
			PermissionHandler:     func(_ net.Addr, peer net.IP) bool { return labPeerAllowed(subnet, peer) },
		}},
		EventHandler: turn.EventHandler{
			OnAllocationCreated: func(_, _ net.Addr, _, _, _ string, _ net.Addr, _ int) {
				logger.Print("Local TURN: allocation created; UDP relay available")
			},
			OnAllocationDeleted: func(_, _ net.Addr, _, _, _ string) { logger.Print("Local TURN: allocation closed") },
			OnPermissionCreated: func(_, _ net.Addr, _, _, _ string, _ net.Addr, _ net.IP) {
				logger.Print("Local TURN: peer permission created")
			},
		},
	})
	if err != nil {
		conn.Close()
		return nil, err
	}
	return t, nil
}
