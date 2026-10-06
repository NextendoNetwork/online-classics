package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStreamsSendInitialFrameAndStayOpen(t *testing.T) {
	server := httptest.NewUnstartedServer(handler(log.New(io.Discard, "", 0)))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	authRequest := protoBytes(nil, 1, []byte("tenants/"+labTenant))
	authRequest = protoBytes(authRequest, 3, protoBytes(nil, 2, []byte("stream-test-user")))
	authResponse := callLabGRPC(t, server.Client(), server.URL+issuePrearrangedUserTokenPath, authRequest, "")
	if authResponse.status != "0" {
		t.Fatalf("Authentication: gRPC %s", authResponse.status)
	}
	var token string
	_ = visitProto(authResponse.body, func(field, wire, _ uint64, value []byte) error {
		if field == 2 && wire == 2 {
			return visitProto(value, func(innerField, innerWire, _ uint64, innerValue []byte) error {
				if innerField == 2 && innerWire == 2 {
					token = string(innerValue)
				}
				return nil
			})
		}
		return nil
	})
	if token == "" {
		t.Fatal("Missing local token")
	}

	cases := []struct {
		path    string
		payload []byte
		bearer  string
		want    []byte
	}{
		{subscribeMaintenancePath, protoBytes(nil, 1, []byte("tenants/"+labTenant)), "", []byte{0x12, 0x00}},
		{subscribeFriendsPath, protoBytes(nil, 1, []byte("tenants/current/users/current")), token, []byte{0x1a, 0x02, 0x08, 0x32}},
		{recvMessagePath, protoBytes(nil, 1, []byte("tenants/current/users/current")), token, []byte{0x1a, 0x04, 0x0a, 0x02, 0x08, 0x5f}},
		{subscribePresencesPath, protoBytes(nil, 1, []byte("tenants/current/users/current")), token, []byte{0x1a, 0x08, 0x0a, 0x02, 0x08, 0x1e, 0x12, 0x02, 0x08, 0x32}},
	}
	for _, test := range cases {
		t.Run(test.path, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			frame := make([]byte, 5, 5+len(test.payload))
			binary.BigEndian.PutUint32(frame[1:5], uint32(len(test.payload)))
			frame = append(frame, test.payload...)
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+test.path, bytes.NewReader(frame))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/grpc")
			request.Header.Set("npln-tenant-id", labTenant)
			if test.bearer != "" {
				request.Header.Set("authorization", "bearer "+test.bearer)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			got := make([]byte, 5+len(test.want))
			if _, err := io.ReadFull(response.Body, got); err != nil {
				t.Fatal(err)
			}
			if got[0] != 0 || int(binary.BigEndian.Uint32(got[1:5])) != len(test.want) || !bytes.Equal(got[5:], test.want) {
				t.Fatalf("Incorrect first message: %x", got)
			}
			if test.path == subscribePresencesPath {
				done := make([]byte, 7)
				if _, err := io.ReadFull(response.Body, done); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(done, []byte{0, 0, 0, 0, 2, 0x12, 0}) {
					t.Fatalf("Missing EnumerationDone: %x", done)
				}
			}
			cancel()
		})
	}
}
