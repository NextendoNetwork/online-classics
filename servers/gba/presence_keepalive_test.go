package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPresenceKeepAliveRespondsBeforeRequestEOF(t *testing.T) {
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	_, token := authenticatePairTest(t, a, "127.0.0.1", "presence-client")
	s := httptest.NewUnstartedServer(http.HandlerFunc(a.keepPresenceAlive))
	s.EnableHTTP2 = true
	s.StartTLS()
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	req, err := http.NewRequestWithContext(ctx, "POST", s.URL+presenceKeepAlivePath, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("npln-tenant-id", labTenant)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := s.Client().Do(req)
	if err != nil {
		t.Fatal("KeepAlive waits for EOF instead of responding:", err)
	}
	defer response.Body.Close()
	first, err := readGRPCStreamFrame(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	// Reference Heartbeat bytes: 30-second interval and 50-second deadline.
	want := []byte{0x0a, 0x08, 0x0a, 0x02, 0x08, 0x1e, 0x12, 0x02, 0x08, 0x32}
	if !bytes.Equal(first, want) {
		t.Fatalf("Incorrect heartbeat: %x", first)
	}
	ack := protoBytes(nil, 2, protoBytes(nil, 1, []byte("tenants/current/users/current")))
	if _, err := writer.Write(streamTestFrame(ack)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(response.Body); err != nil {
		t.Fatal(err)
	}
	if status := response.Trailer.Get("Grpc-Status"); status != "0" {
		t.Fatalf("Ack rejected: %s", status)
	}
}

func TestPresenceKeepAliveRejectsOtherUsersAndMultipleOperations(t *testing.T) {
	own := "tenants/" + labTenant + "/users/u-owner"
	ack := protoBytes(nil, 2, protoBytes(nil, 1, []byte(own)))
	update := protoBytes(nil, 1, protoBytes(nil, 1, protoBytes(nil, 1, []byte(own+"/presence"))))
	for _, data := range [][]byte{ack, update} {
		if _, err := validatePresenceKeepAlive(data, "u-owner"); err != nil {
			t.Fatal(err)
		}
	}
	for _, data := range [][]byte{nil, append(append([]byte(nil), ack...), update...), protoBytes(nil, 2, protoBytes(nil, 1, []byte("tenants/"+labTenant+"/users/u-other"))), protoBytes(nil, 1, protoBytes(nil, 1, protoBytes(nil, 1, []byte("tenants/"+labTenant+"/users/u-other/presence"))))} {
		if _, err := validatePresenceKeepAlive(data, "u-owner"); err == nil {
			t.Fatal("Unauthorized or invalid operation accepted")
		}
	}
}
