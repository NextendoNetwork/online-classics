package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func streamTestFrame(payload []byte) []byte {
	frame := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(frame[1:], uint32(len(payload)))
	return append(frame, payload...)
}

func TestKeepSessionEchoBeforeRequestEOFOverHTTP2(t *testing.T) {
	s := newTicketStore()
	ticket := pendingTicket{userName: "tenants/" + labTenant + "/gameSessions/room/userSessions/player"}
	s.issuedGamesync[sha256.Sum256([]byte("private-access"))] = issuedMatch{ticket: ticket, expires: time.Now().Add(time.Hour)}
	server := httptest.NewUnstartedServer(s.keepGamesyncSession(log.New(io.Discard, "", 0)))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	req, err := http.NewRequestWithContext(ctx, "POST", server.URL+gamesyncKeepUserSessionPath, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Authorization", "Bearer private-access")
	// The producer writes one frame and waits for permission to send the second.
	// The request body remains open when the first response arrives.
	next := make(chan struct{}, 1)
	writeResult := make(chan error, 1)
	go func() {
		for i, echo := range []string{"echo-private-one", "echo-private-two"} {
			if i != 0 {
				select {
				case <-next:
				case <-ctx.Done():
					writeResult <- ctx.Err()
					return
				}
			}
			// A different channel name must not prevent an authenticated echo.
			payload := protoBytes(nil, 1, []byte("connection-channel"))
			payload = protoBytes(payload, 2, []byte(echo))
			frame := streamTestFrame(payload)
			// Separate header and body writes simulate partial reads.
			for _, piece := range [][]byte{frame[:2], frame[2:5], frame[5:]} {
				if _, err := writer.Write(piece); err != nil {
					writeResult <- err
					return
				}
			}
		}
		select {
		case <-next:
			writeResult <- writer.Close()
		case <-ctx.Done():
			writeResult <- ctx.Err()
		}
	}()
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.ProtoMajor != 2 {
		t.Fatal("Test requires HTTP/2")
	}
	for _, echo := range []string{"echo-private-one", "echo-private-two"} {
		payload, err := readGRPCStreamFrame(res.Body)
		if err != nil || !bytes.Equal(payload, protoBytes(nil, 1, []byte(echo))) {
			t.Fatalf("Echo not received while body remained open: %v", err)
		}
		next <- struct{}{}
	}
	if err := <-writeResult; err != nil {
		t.Fatal(err)
	}
	if _, err := readGRPCStreamFrame(res.Body); err != io.EOF || res.Trailer.Get("Grpc-Status") != "0" {
		t.Fatalf("Unexpected closure: %v, status=%s", err, res.Trailer.Get("Grpc-Status"))
	}
}

func TestKeepSessionRejectsForeignSessionAndUnknownAccess(t *testing.T) {
	for _, tc := range []struct{ token, session string }{
		{"known-access", "userSessions/other-player"},
		{"forged-access", "userSessions/player"},
	} {
		s := newTicketStore()
		s.issuedGamesync[sha256.Sum256([]byte("known-access"))] = issuedMatch{
			ticket: pendingTicket{userName: "tenants/" + labTenant + "/gameSessions/room/userSessions/player"}, expires: time.Now().Add(time.Hour),
		}
		payload := protoBytes(nil, 1, []byte(tc.session))
		// Target operations still require the caller's own participant identity.
		payload = protoBytes(payload, 3, []byte("private-target-request"))
		req := httptest.NewRequest("POST", gamesyncKeepUserSessionPath, bytes.NewReader(streamTestFrame(payload)))
		req.Header.Set("Content-Type", "application/grpc")
		req.Header.Set("Authorization", "Bearer "+tc.token)
		var output bytes.Buffer
		rr := httptest.NewRecorder()
		s.keepGamesyncSession(log.New(&output, "", 0))(rr, req)
		res := rr.Result()
		res.Body.Close()
		if res.Trailer.Get("Grpc-Status") != "16" {
			t.Fatal("Accepted an unauthorized session or token")
		}
		if strings.Contains(output.String(), tc.token) || strings.Contains(output.String(), "private-target-request") {
			t.Fatal("Private content was logged")
		}
	}
}

func TestStreamFrameBoundsAndTruncation(t *testing.T) {
	for _, frame := range [][]byte{{0, 0}, {1, 0, 0, 0, 0}, {0, 0, 1, 0, 1}, {0, 0, 0, 0, 2, 1}} {
		if _, err := readGRPCStreamFrame(bytes.NewReader(frame)); err == nil {
			t.Fatal("Accepted a compressed, oversized or truncated frame")
		}
	}
}

func TestTargetDiagnosticMasksUnknownPathSegments(t *testing.T) {
	var output bytes.Buffer
	docs := protoBytes(nil, 1, []byte("docs/__us/PRIVATE_PLAYER_ID"))
	target := protoBytes(nil, 1, []byte("PRIVATE_TARGET_ID"))
	target = protoBytes(target, 2, docs)
	name, err := inspectGamesyncTarget(protoBytes(nil, 1, target), log.New(&output, "", 0))
	if err != nil || name != "PRIVATE_TARGET_ID" || !strings.Contains(output.String(), "docs/__us/{id}") || strings.Contains(output.String(), "PRIVATE") {
		t.Fatal("Incorrect target diagnostic")
	}
}

func TestPendingTargetReportsFailureAndKeepsEchoUsable(t *testing.T) {
	s := newTicketStore()
	s.issuedGamesync[sha256.Sum256([]byte("private-access"))] = issuedMatch{
		ticket: pendingTicket{userName: "tenants/" + labTenant + "/gameSessions/room/userSessions/player"}, expires: time.Now().Add(time.Hour),
	}
	// Pending system document: FAILED/12, not success.
	docs := protoBytes(nil, 1, []byte("docs/__gs/m"))
	target := protoBytes(nil, 1, []byte("PRIVATE_TARGET_ID"))
	target = protoBytes(target, 2, docs)
	watch := protoBytes(nil, 1, []byte("userSessions/player"))
	watch = protoBytes(watch, 3, protoBytes(nil, 1, target))
	echo := protoBytes(nil, 1, []byte("userSessions/player"))
	echo = protoBytes(echo, 2, []byte("private-echo"))
	body := append(streamTestFrame(watch), streamTestFrame(echo)...)
	req := httptest.NewRequest("POST", gamesyncKeepUserSessionPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Authorization", "Bearer private-access")
	var logs bytes.Buffer
	rr := httptest.NewRecorder()
	s.keepGamesyncSession(log.New(&logs, "", 0))(rr, req)
	first, err := readGRPCStreamFrame(rr.Body)
	if err != nil {
		t.Fatal(err)
	}
	var change []byte
	_ = visitProto(first, func(f, w, _ uint64, v []byte) error {
		if f == 3 && w == 2 {
			change = v
		}
		return nil
	})
	var state, code uint64
	_ = visitProto(change, func(f, w, n uint64, v []byte) error {
		if f == 2 && w == 0 {
			state = n
		}
		if f == 3 && w == 2 {
			return visitProto(v, func(f, w, n uint64, _ []byte) error {
				if f == 1 && w == 0 {
					code = n
				}
				return nil
			})
		}
		return nil
	})
	second, err := readGRPCStreamFrame(rr.Body)
	if state != 4 || code != 12 || err != nil || !bytes.Equal(second, protoBytes(nil, 1, []byte("private-echo"))) {
		t.Fatal("Pending target must report FAILED/12 and allow echo")
	}
	for _, secret := range []string{"private-access", "private-echo", "PRIVATE_TARGET_ID", "PRIVATE_PLAYER_ID"} {
		if strings.Contains(logs.String(), secret) {
			t.Fatal("Diagnostic published private content")
		}
	}
}
