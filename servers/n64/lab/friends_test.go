package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestActivateUserAcceptsIssuedTokenAndRejectsOtherToken(t *testing.T) {
	server := httptest.NewUnstartedServer(handler(log.New(io.Discard, "", 0)))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	authRequest := protoBytes(nil, 1, []byte("tenants/"+labTenant))
	authRequest = protoBytes(authRequest, 3, protoBytes(nil, 2, []byte("local-test-user")))
	authResponse := callLabGRPC(t, server.Client(), server.URL+issuePrearrangedUserTokenPath, authRequest, "")
	if authResponse.status != "0" {
		t.Fatalf("Authentication: gRPC %s", authResponse.status)
	}
	var token string
	if err := visitProto(authResponse.body, func(field, wire, _ uint64, value []byte) error {
		if field == 2 && wire == 2 {
			return visitProto(value, func(innerField, innerWire, _ uint64, innerValue []byte) error {
				if innerField == 2 && innerWire == 2 {
					token = string(innerValue)
				}
				return nil
			})
		}
		return nil
	}); err != nil || token == "" {
		t.Fatal("Authentication response missing token")
	}

	activate := protoBytes(nil, 1, []byte("tenants/current/users/current"))
	good := callLabGRPC(t, server.Client(), server.URL+activateUserPath, activate, token)
	if good.status != "0" {
		t.Fatalf("ActivateUser with valid token: gRPC %s", good.status)
	}
	bad := callLabGRPC(t, server.Client(), server.URL+activateUserPath, activate, token+"x")
	if bad.status != "16" {
		t.Fatalf("ActivateUser with changed token: gRPC %s", bad.status)
	}
}

type labAnswer struct {
	status string
	body   []byte
}

func callLabGRPC(t *testing.T, client *http.Client, url string, payload []byte, token string) labAnswer {
	t.Helper()
	frame := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	frame = append(frame, payload...)
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(frame))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("npln-tenant-id", labTenant)
	if token != "" {
		request.Header.Set("authorization", "bearer "+token)
	}
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
		t.Fatalf("Expected HTTP/2: %s", response.Proto)
	}
	if len(body) >= 5 {
		body = body[5:]
	}
	return labAnswer{status: response.Trailer.Get("Grpc-Status"), body: body}
}
