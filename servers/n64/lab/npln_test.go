package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQueryGameSessionsReturnsLabRoomInGRPC(t *testing.T) {
	store := newRoomStore()
	store.rooms["abc123"] = Room{ID: "abc123", Host: "Ryujinx", Players: []string{"Ryujinx"}}
	mux := http.NewServeMux()
	store.register(mux)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	request, err := http.NewRequest(http.MethodPost, server.URL+queryGameSessionsPath, bytes.NewReader([]byte{0, 0, 0, 0, 0}))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("npln-tenant-id", labTenant)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	frame, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.ProtoMajor != 2 || response.Trailer.Get("Grpc-Status") != "0" {
		t.Fatalf("Unexpected response: %s, gRPC %s", response.Proto, response.Trailer.Get("Grpc-Status"))
	}
	if len(frame) < 5 || int(binary.BigEndian.Uint32(frame[1:5])) != len(frame)-5 {
		t.Fatal("Invalid gRPC framing")
	}
	if !bytes.Contains(frame[5:], []byte("tenants/"+labTenant+"/gameSessions/abc123")) {
		t.Fatal("Protobuf response does not contain the room")
	}
}

func TestQueryGameSessionsAcceptsRealQueryWithoutInventingRooms(t *testing.T) {
	store := newRoomStore()
	store.rooms["mock"] = Room{ID: "mock", Host: "probe", Players: []string{"probe"}}
	mux := http.NewServeMux()
	store.register(mux)
	payload := protoBytes(nil, 1, []byte("tenants/"+labTenant))
	payload = protoBytes(payload, 3, []byte("genesis"))
	frame := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	frame = append(frame, payload...)
	request := httptest.NewRequest(http.MethodPost, queryGameSessionsPath, bytes.NewReader(frame))
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("npln-tenant-id", labTenant)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Result().Trailer.Get("Grpc-Status") != "0" || !bytes.Equal(response.Body.Bytes(), []byte{0, 0, 0, 0, 0}) {
		t.Fatalf("Expected empty list with gRPC 0; received status=%s body=%x", response.Result().Trailer.Get("Grpc-Status"), response.Body.Bytes())
	}
}
