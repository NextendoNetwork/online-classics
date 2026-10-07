package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginDeviceTokenDeliveryIsPrivateAndPreservesBody(t *testing.T) {
	a, _ := newLabAuth()
	a.friendPair, _ = newLocalFriendPair("127.0.0.1,10.77.20.92")
	a.messages = newLocalMessageStore()
	host, hostToken := authenticatePairTest(t, a, "127.0.0.1", "host")
	guest, guestToken := authenticatePairTest(t, a, "10.77.20.92", "guest")
	mux := http.NewServeMux()
	mux.HandleFunc(sendMessagePath, a.inspectSendMessage(quiet()))
	mux.HandleFunc(recvMessagePath, a.recvMessage)
	s := httptest.NewUnstartedServer(mux)
	s.EnableHTTP2 = true
	s.StartTLS()
	defer s.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", s.URL+recvMessagePath, bytes.NewReader(streamTestFrame(protoBytes(nil, 1, []byte("tenants/current/users/current")))))
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("npln-tenant-id", labTenant)
	request.Header.Set("Authorization", "Bearer "+hostToken)
	response, err := s.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := readGRPCStreamFrame(response.Body); err != nil {
		t.Fatal(err)
	}
	body := protoBytes(nil, 1, []byte("fake-request-id"))
	body = protoBytes(body, 3, protoBytes(nil, 1, protoBytes(nil, 1, []byte("fake-private-device-token"))))
	body = protoBytes(body, 4, []byte("LoginDeviceToken"))
	send := func(receiver, token string) string {
		payload := protoBytes(nil, 1, []byte("tenants/current/users/current"))
		payload = protoBytes(payload, 2, []byte(receiver))
		payload = protoBytes(payload, 3, body)
		return callLabGRPC(t, s.Client(), s.URL+sendMessagePath, payload, token).status
	}
	if send("tenants/current/users/"+host, hostToken) != "0" {
		t.Fatal("Own alias rejected")
	}
	frame, err := readGRPCStreamFrame(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	envelope := decodeTestMessage(t, decodeTestMessage(t, frame)[1])
	if !bytes.Equal(envelope[1], body) || string(envelope[2]) != "tenants/"+labTenant+"/users/"+host {
		t.Fatal("Body or sender changed")
	}
	if _, ok, _ := a.messages.next(guest); ok {
		t.Fatal("Device token delivered to the other client")
	}
	if send("tenants/current/users/"+guest, hostToken) != "12" {
		t.Fatal("Device token accepted for another account")
	}
	if send("tenants/current/users/"+host, guestToken) != "12" {
		t.Fatal("Second account sent a device token to the first")
	}
	if _, ok, _ := a.messages.next(guest); ok {
		t.Fatal("Rejected delivery was queued")
	}
}

func TestLocalMessageQueueHasBoundAndExpiry(t *testing.T) {
	s := newLocalMessageStore()
	for i := 0; i < 8; i++ {
		if !s.enqueue("u-test", []byte("x")) {
			t.Fatal("Queue filled prematurely")
		}
	}
	if s.enqueue("u-test", []byte("x")) {
		t.Fatal("Unbounded queue")
	}
	s.mu.Lock()
	for i := range s.boxes["u-test"].queue {
		s.boxes["u-test"].queue[i].sent = time.Now().Add(-2 * time.Minute)
	}
	s.mu.Unlock()
	if _, ok, _ := s.next("u-test"); ok {
		t.Fatal("Delivered an expired message")
	}
	if !s.enqueue("u-test", []byte("x")) {
		t.Fatal("Expired queue was not released")
	}
	s.close("u-test")
	if _, ok, _ := s.next("u-test"); ok {
		t.Fatal("Closure retained secrets")
	}
}
