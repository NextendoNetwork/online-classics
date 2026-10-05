package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"testing"
	"time"
)

type udpDiagnosticTestWriter struct{ lines chan string }

func (w udpDiagnosticTestWriter) Write(p []byte) (int, error) {
	w.lines <- string(p)
	return len(p), nil
}

func TestUDPDiagnosticsReceiveFourDestinationsWithoutReplyOrPayload(t *testing.T) {
	lines := make(chan string, 64)
	logger := log.New(udpDiagnosticTestWriter{lines}, "", 0)
	// Real STUN on a distinct loopback address stands in for the LAN listener.
	stun, endpoint, err := startLocalSTUN("127.0.0.3:0", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer stun.Close()
	reserved, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.3")})
	if err != nil {
		t.Fatal(err)
	}
	port := reserved.LocalAddr().(*net.UDPAddr).Port
	reserved.Close()
	conns, err := startLoopbackUDPDiagnostics([]int{endpoint.port, port, port}, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer closeUDPDiagnostics(conns)
	if len(conns) != 4 {
		t.Fatal("expected four unique listeners")
	}
	for _, conn := range conns {
		client, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
		client.SetDeadline(time.Now().Add(150 * time.Millisecond))
		if _, err = client.Write([]byte("private-payload-secret")); err != nil {
			client.Close()
			t.Fatal(err)
		}
		var b [128]byte
		if _, err = client.Read(b[:]); err == nil {
			client.Close()
			t.Fatal("diagnostic listener responded")
		} else if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
			client.Close()
			t.Fatalf("expected unanswered datagram timeout: %v", err)
		}
		client.Close()
	}
	var received int
	for received < 4 {
		select {
		case line := <-lines:
			if strings.Contains(line, "private-") {
				t.Fatal("payload exposed")
			}
			if strings.Contains(line, "bytes=") {
				if !strings.Contains(line, "source_class=loopback") || !strings.Contains(line, "source_port=") || !strings.Contains(line, "reply=false") {
					t.Fatal("missing metadata")
				}
				received++
			}
		case <-time.After(time.Second):
			t.Fatal("missing diagnostic receipt")
		}
	}
	client, err := net.DialUDP("udp4", nil, stun.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(time.Second))
	client.Write(bindingRequest())
	var reply [128]byte
	n, err := client.Read(reply[:])
	if err != nil || n != 40 || reply[0] != 1 || reply[1] != 1 {
		t.Fatal("real STUN no longer responds")
	}
}

func TestUDPDiagnosticsRejectNonLoopbackAndRollbackStartup(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", "192.168.100.3:0", "8.8.8.8:0"} {
		if conn, err := startUDPDiagnostic(address, quiet()); err == nil {
			conn.Close()
			t.Fatal("accepted non-loopback listener")
		}
	}
	occupied, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.2")})
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	port := occupied.LocalAddr().(*net.UDPAddr).Port
	if conns, err := startLoopbackUDPDiagnostics([]int{port}, quiet()); err == nil || len(conns) != 0 {
		closeUDPDiagnostics(conns)
		t.Fatal("conflict did not fail cleanly")
	}
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatal("startup rollback left first listener open")
	}
	probe.Close()
	for _, tc := range []struct{ ip, class string }{{"127.0.0.2", "loopback"}, {"192.168.100.3", "private"}, {"8.8.8.8", "other"}} {
		if udpSourceClass(net.ParseIP(tc.ip)) != tc.class {
			t.Fatal("incorrect source classification")
		}
	}
}

func TestSessionUDPDiagnosticObservesWithoutReplyAndRollsBack(t *testing.T) {
	lines := make(chan string, 64)
	logger := log.New(udpDiagnosticTestWriter{lines}, "", 0)
	conns, err := startSessionUDPDiagnostics([]string{"127.0.0.1:0", "127.0.0.2:0"}, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer closeUDPDiagnostics(conns)
	for _, conn := range conns {
		client, err := net.DialUDP("udp4", nil, conn.LocalAddr().(*net.UDPAddr))
		if err != nil {
			t.Fatal(err)
		}
		client.SetDeadline(time.Now().Add(100 * time.Millisecond))
		if _, err := client.Write([]byte("session-private-payload")); err != nil {
			client.Close()
			t.Fatal(err)
		}
		var b [64]byte
		_, err = client.Read(b[:])
		client.Close()
		if ne, ok := err.(net.Error); !ok || !ne.Timeout() {
			t.Fatalf("expected no reply: %v", err)
		}
	}
	count := 0
	for count < 2 {
		select {
		case line := <-lines:
			if strings.Contains(line, "session-private-payload") {
				t.Fatal("payload leaked")
			}
			if strings.Contains(line, "bytes=23") && strings.Contains(line, "reply=false") {
				count++
			}
		case <-time.After(time.Second):
			t.Fatal("missing observations")
		}
	}
	port := conns[1].LocalAddr().(*net.UDPAddr).Port
	if result, err := startSessionUDPDiagnostics([]string{net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), net.JoinHostPort("127.0.0.2", fmt.Sprint(port))}, quiet()); err == nil || len(result) != 0 {
		closeUDPDiagnostics(result)
		t.Fatal("conflict must fail and rollback")
	}
	probe, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: port})
	if err != nil {
		t.Fatal("rollback left listener open")
	}
	probe.Close()
	for _, address := range []string{"0.0.0.0:443", "8.8.8.8:443"} {
		if result, err := startSessionUDPDiagnostics([]string{address}, quiet()); err == nil {
			closeUDPDiagnostics(result)
			t.Fatal("accepted non-local address")
		}
	}
}
