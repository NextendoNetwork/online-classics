package main

import (
	"bytes"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v5"
)

type lockedTURNLog struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedTURNLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *lockedTURNLog) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }

func TestTURNAdvertisedCredentialsAllocateAndRelay(t *testing.T) {
	var logs lockedTURNLog
	logger := log.New(&logs, "", 0)
	relay, err := startLocalTURN("127.0.0.1:0", logger)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	stun, endpoint, err := startLocalSTUN("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	defer stun.Close()
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	claims := labClaims{Subject: "PRIVATE_USER", Expires: time.Now().Add(time.Hour).Unix()}
	claims.NPLN.Tenant = labTenant
	token, err := a.signJWT(claims)
	if err != nil {
		t.Fatal(err)
	}
	payload := protoBytes(nil, 1, []byte("tenants/current"))
	r := httptest.NewRequest("POST", allocateIceServerSetPath, bytes.NewReader(streamTestFrame(payload)))
	r.Header.Set("Content-Type", "application/grpc")
	r.Header.Set("npln-tenant-id", labTenant)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.allocateIceServerSet(endpoint, logger, relay)(w, r)
	if w.Result().Trailer.Get("Grpc-Status") != "0" {
		t.Fatal("AllocateIceServerSet rejected")
	}
	server := decodeTestMessage(t, decodeTestMessage(t, w.Body.Bytes()[5:])[3])
	username, password := string(server[4]), string(server[5])
	if string(server[1]) != "127.0.0.1" || username == "" || password == "" || !validTURNUsername(username, time.Now()) {
		t.Fatal("Invalid TURN announcement")
	}
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	factory := logging.NewDefaultLoggerFactory()
	factory.Writer = io.Discard
	client, err := turn.NewClient(&turn.ClientConfig{Conn: conn, TURNServerAddr: net.JoinHostPort(relay.endpoint.host, strconv.Itoa(relay.endpoint.port)), Username: username, Password: password, Realm: localTURNRealm, RTO: 20 * time.Millisecond, LoggerFactory: factory})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err = client.Listen(); err != nil {
		t.Fatal(err)
	}
	allocated, err := client.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	defer allocated.Close()
	if !allocated.LocalAddr().(*net.UDPAddr).IP.IsLoopback() {
		t.Fatal("Relay is not local")
	}
	peer, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peer.SetDeadline(time.Now().Add(2 * time.Second))
	allocated.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err = allocated.WriteTo([]byte("PRIVATE_RELAY_PAYLOAD"), peer.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 256)
	n, from, err := peer.ReadFrom(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(b[:n]) != "PRIVATE_RELAY_PAYLOAD" || from.String() != allocated.LocalAddr().String() {
		t.Fatal("Relay did not transmit correctly")
	}
	if _, err = peer.WriteTo([]byte("reply"), from); err != nil {
		t.Fatal(err)
	}
	n, _, err = allocated.ReadFrom(b)
	if err != nil || string(b[:n]) != "reply" {
		t.Fatal("Relay did not receive a reply", err)
	}
	for _, secret := range []string{username, password, token, "PRIVATE_USER", "PRIVATE_RELAY_PAYLOAD"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("Log contains private data")
		}
	}
	if !strings.Contains(logs.String(), "allocation created") || !strings.Contains(logs.String(), "TURN=1") {
		t.Fatal("Missing actual-service evidence")
	}
}

func TestTURNCredentialsExpireAndNonLocalListenerIsRejected(t *testing.T) {
	relay, err := startLocalTURN("127.0.0.1:0", quiet())
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	now := time.Now()
	user, pass, err := relay.credentials(now)
	if err != nil {
		t.Fatal(err)
	}
	if !validTURNUsername(user, now) || validTURNUsername(user, now.Add(2*time.Hour)) || validTURNUsername("9999999999999:fake", now) {
		t.Fatal("Incorrect expiration")
	}
	if pass != relay.password(user) || pass == relay.password(user+"different") {
		t.Fatal("Credential not bound to user")
	}
	if service, err := startLocalTURN("0.0.0.0:0", quiet()); err == nil {
		service.Close()
		t.Fatal("Nonlocal listener permitted")
	}
}
