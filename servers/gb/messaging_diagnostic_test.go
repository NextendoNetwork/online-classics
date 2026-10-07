package main

import (
	"bytes"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMessagingDiagnosticDoesNotExposeDeviceTokenOrMessageID(t *testing.T) {
	a, _ := newLabAuth()
	_, token := authenticatePairTest(t, a, "127.0.0.1", "auth-secret")
	var diagnostic, logs bytes.Buffer
	a.identityDiagnostic = &identityDiagnostic{out: &diagnostic}
	payload := protoBytes(nil, 1, []byte("tenants/current/users/current"))
	payload = protoBytes(payload, 2, []byte("tenants/current/users/backend-user"))
	body := protoBytes(nil, 1, []byte("private-message-id"))
	body = protoVarint(body, 2, 1)
	body = protoBytes(body, 3, protoBytes(nil, 1, []byte("private-device-token")))
	body = protoBytes(body, 4, []byte("LoginDeviceToken"))
	payload = protoBytes(payload, 3, body)
	r := httptest.NewRequest("POST", sendMessagePath, bytes.NewReader(streamTestFrame(payload)))
	r.RemoteAddr = "10.77.20.92:12345"
	r.Header.Set("Content-Type", "application/grpc")
	r.Header.Set("npln-tenant-id", labTenant)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	a.inspectSendMessage(log.New(&logs, "", 0))(w, r)
	combined := logs.String() + diagnostic.String()
	for _, secret := range []string{"private-message-id", "private-device-token", token, "auth-secret"} {
		if strings.Contains(combined, secret) {
			t.Fatal("Diagnostic exposed a secret")
		}
	}
	if !strings.Contains(diagnostic.String(), "backend-user") || !strings.Contains(logs.String(), "others=1 requires_ack=true") {
		t.Fatal("Missing route or acknowledgment requirement")
	}
	if w.Result().Trailer.Get("Grpc-Status") != "12" {
		t.Fatal("Diagnostic simulated message delivery")
	}
}
