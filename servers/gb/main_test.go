package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuthUsesHTTP2AndReturnsLocalToken(t *testing.T) {
	var logs bytes.Buffer
	server := httptest.NewUnstartedServer(handler(log.New(&logs, "", 0)))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	client := server.Client()
	payload := protoBytes(nil, 1, []byte("tenants/"+labTenant))
	payload = protoBytes(payload, 3, protoBytes(nil, 2, []byte("secret-for-test")))
	frame := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	frame = append(frame, payload...)
	request, err := http.NewRequest(http.MethodPost, server.URL+issuePrearrangedUserTokenPath, bytes.NewReader(frame))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("npln-tenant-id", "t-7b4e32ca-lp1")
	request.Header.Set("authorization", "secret-for-test")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.ProtoMajor != 2 {
		t.Fatalf("Expected HTTP/2; received %s", response.Proto)
	}
	if got := response.Trailer.Get("Grpc-Status"); got != "0" {
		t.Fatalf("Expected gRPC OK (0); received %q", got)
	}
	if len(body) < 5 || int(binary.BigEndian.Uint32(body[1:5])) != len(body)-5 ||
		!bytes.Contains(body, []byte("tenants/"+labTenant+"/users/u-")) ||
		!bytes.Contains(body, []byte("eyJ")) {
		t.Fatal("Protobuf response missing user or token")
	}
	if !strings.Contains(logs.String(), "IssuePrearrangedUserToken") || strings.Contains(logs.String(), "secret-for-test") {
		t.Fatalf("Incorrect log: %s", logs.String())
	}
}
