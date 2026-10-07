package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func presenceUpdateTest(name string, state uint64, attributes map[string]string, mask ...string) []byte {
	p := protoVarint(protoBytes(nil, 1, []byte(name)), 2, state)
	for key, value := range attributes {
		entry := protoBytes(nil, 1, []byte(key))
		entry = protoBytes(entry, 2, protoBytes(nil, 3, []byte(value)))
		p = protoBytes(p, 3, entry)
	}
	u := protoBytes(nil, 1, p)
	if len(mask) > 0 {
		var m []byte
		for _, path := range mask {
			m = protoBytes(m, 1, []byte(path))
		}
		u = protoBytes(u, 2, m)
	}
	return protoBytes(nil, 1, u)
}

func TestLocalPresenceLiveDeliveryAndDisconnect(t *testing.T) {
	pair, _ := newLocalFriendPair("127.0.0.1,10.77.20.92")
	a, _ := newLabAuth()
	a.friendPair = pair
	a.presences = newLocalPresenceStore()
	host, hostToken := authenticatePairTest(t, a, "127.0.0.1", "host")
	_, guestToken := authenticatePairTest(t, a, "10.77.20.92", "guest")
	mux := http.NewServeMux()
	mux.HandleFunc(presenceKeepAlivePath, a.keepPresenceAlive)
	mux.HandleFunc(subscribePresencesPath, a.subscribePresences)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	request := func(path, token string, body io.Reader) *http.Response {
		t.Helper()
		r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+path, body)
		r.Header.Set("Content-Type", "application/grpc")
		r.Header.Set("npln-tenant-id", labTenant)
		r.Header.Set("Authorization", "Bearer "+token)
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	reader, writer := io.Pipe()
	defer writer.Close()
	keep := request(presenceKeepAlivePath, hostToken, reader)
	defer keep.Body.Close()
	keepHeartbeat, err := readGRPCStreamFrame(keep.Body)
	if err != nil {
		t.Fatal(err)
	}
	keepFields := decodeTestMessage(t, decodeTestMessage(t, keepHeartbeat)[1])
	if !bytes.Equal(keepFields[1], protoVarint(nil, 1, 30)) || !bytes.Equal(keepFields[2], protoVarint(nil, 1, 50)) {
		t.Fatal("Heartbeat missing the reference interval or deadline")
	}
	query := protoBytes(nil, 1, []byte("tenants/current/users/current"))
	var mask []byte
	for _, path := range []string{"name", "state", "attributes", "last_online_time", "unsubscribed", "debug_info"} {
		mask = protoBytes(mask, 1, []byte(path))
	}
	query = protoBytes(query, 3, mask)
	query = protoVarint(query, 4, ^uint64(0))
	sub := request(subscribePresencesPath, guestToken, bytes.NewReader(streamTestFrame(query)))
	defer sub.Body.Close()
	if _, err := readGRPCStreamFrame(sub.Body); err != nil {
		t.Fatal(err)
	}
	readPresence := func() localPresence {
		t.Helper()
		frame, err := readGRPCStreamFrame(sub.Body)
		if err != nil {
			t.Fatal(err)
		}
		outer := decodeTestMessage(t, frame)
		list := decodeTestMessage(t, outer[1])
		cursor, cursorErr := base64.StdEncoding.DecodeString(string(list[2]))
		if cursorErr != nil || len(cursor) == 0 {
			t.Fatal("Presence batch missing enumeration cursor")
		}
		entry := decodeTestMessage(t, decodeTestMessage(t, cursor)[1])
		if string(entry[1]) != host {
			t.Fatal("Cursor does not match the batch's only authorized friend")
		}
		p, err := parsePresence(list[1])
		if err != nil || p.name == "" {
			t.Fatal("Missing presence", err)
		}
		return p
	}
	initial := readPresence()
	if initial.name != localPresenceName(host) || initial.state != localPresenceOnline || len(initial.attrs) != 0 || len(initial.lastOnline) != 0 {
		t.Fatalf("KeepAlive did not publish ONLINE before the update: %+v", initial)
	}
	done, err := readGRPCStreamFrame(sub.Body)
	if err != nil || !bytes.Equal(done, protoBytes(nil, 2, nil)) {
		t.Fatal("Incorrect initial enumeration", err)
	}
	update := presenceUpdateTest("tenants/current/users/current/presence", 1, map[string]string{"game": "genesis", "room": "room-A"})
	if _, err := writer.Write(streamTestFrame(update)); err != nil {
		t.Fatal(err)
	}
	got := readPresence()
	if got.name != "tenants/"+labTenant+"/users/"+host+"/presence" || got.state != 1 || len(got.attrs) != 2 {
		t.Fatalf("Friend was not published: %+v", got)
	}
	update = presenceUpdateTest("tenants/current/users/current/presence", 0, nil, "attributes.room")
	if _, err := writer.Write(streamTestFrame(update)); err != nil {
		t.Fatal(err)
	}
	got = readPresence()
	if got.state != 1 || len(got.attrs) != 1 || got.attrs["game"] == nil {
		t.Fatal("Mask deleted unrequested fields")
	}
	writer.Close()
	io.Copy(io.Discard, keep.Body)
	got = readPresence()
	if got.state != 2 || len(got.lastOnline) == 0 {
		t.Fatal("Disconnection did not publish OFFLINE")
	}
}

func TestLocalPresenceCannotPublishAfterLeaseCloses(t *testing.T) {
	s := newLocalPresenceStore()
	lease := s.open("u-owner")
	data := presenceUpdateTest("tenants/current/users/current/presence", 1, nil)
	s.close("u-owner", lease)
	if s.update("u-owner", lease, data) == nil {
		t.Fatal("Closed reader revived presence")
	}
	if p := s.entries["u-owner"]; p.state != localPresenceOffline || len(p.lastOnline) == 0 || len(p.attrs) != 0 {
		t.Fatal("Closed reader altered OFFLINE presence")
	}
}

func TestLocalPresenceStateIsDerivedFromLeases(t *testing.T) {
	s := newLocalPresenceStore()
	uid := "u-owner"
	first := s.open(uid)
	second := s.open(uid)
	// Realistic update without state, name or mask; previously cleared ONLINE.
	entry := protoBytes(nil, 1, []byte("game"))
	entry = protoBytes(entry, 2, protoBytes(nil, 3, []byte("genesis")))
	data := protoBytes(nil, 1, protoBytes(nil, 1, protoBytes(nil, 3, entry)))
	if err := s.update(uid, first, data); err != nil {
		t.Fatal(err)
	}
	assertOnline := func() {
		t.Helper()
		p := s.entries[uid]
		if p.name != localPresenceName(uid) || p.state != localPresenceOnline || len(p.lastOnline) != 0 || !bytes.Equal(p.attrs["game"], entry) {
			t.Fatalf("Incorrect active presence: %+v", p)
		}
	}
	assertOnline()
	// An attributes mask without state must preserve channel state.
	u := protoBytes(nil, 1, protoBytes(nil, 3, entry))
	u = protoBytes(u, 2, protoBytes(nil, 1, []byte("attributes")))
	if err := s.update(uid, first, protoBytes(nil, 1, u)); err != nil {
		t.Fatal(err)
	}
	assertOnline()
	for _, state := range []uint64{0, 2, 3} {
		if err := s.update(uid, first, presenceUpdateTest("tenants/current/users/current", state, nil, "state")); err != nil {
			t.Fatal(err)
		}
		assertOnline()
	}
	s.close(uid, first)
	assertOnline()
	s.close(uid, second)
	offline := s.entries[uid]
	if offline.state != localPresenceOffline || len(offline.lastOnline) == 0 {
		t.Fatal("Last lease did not publish dated OFFLINE presence")
	}
	s.close(uid, second) // Repeated closure must not change the timestamp or notify.
	if !bytes.Equal(s.entries[uid].lastOnline, offline.lastOnline) {
		t.Fatal("Repeated closure changed the timestamp")
	}
	third := s.open(uid)
	assertOnline()
	s.close(uid, first) // An old reader must not disconnect the new channel either.
	assertOnline()
	s.close(uid, third)
}

func TestLocalPresenceEnumeratesDeclaredOfflineFriend(t *testing.T) {
	pair, _ := newLocalFriendPair("127.0.0.1,10.77.20.92")
	a, _ := newLabAuth()
	a.friendPair = pair
	a.presences = newLocalPresenceStore()
	host, _ := authenticatePairTest(t, a, "127.0.0.1", "host")
	_, guestToken := authenticatePairTest(t, a, "10.77.20.92", "guest")
	a.presences.open("u-outsider") // Never enumerate anyone outside the authorized friend pair.
	mux := http.NewServeMux()
	mux.HandleFunc(subscribePresencesPath, a.subscribePresences)
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	query := protoBytes(nil, 1, []byte("tenants/current/users/current"))
	r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+subscribePresencesPath, bytes.NewReader(streamTestFrame(query)))
	r.Header.Set("Content-Type", "application/grpc")
	r.Header.Set("npln-tenant-id", labTenant)
	r.Header.Set("Authorization", "Bearer "+guestToken)
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := readGRPCStreamFrame(response.Body); err != nil {
		t.Fatal(err)
	}
	read := func(state uint64) {
		t.Helper()
		frame, err := readGRPCStreamFrame(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		list := decodeTestMessage(t, decodeTestMessage(t, frame)[1])
		var count int
		if err := visitProto(decodeTestMessage(t, frame)[1], func(f, w, _ uint64, _ []byte) error {
			if f == 1 && w == 2 {
				count++
			}
			return nil
		}); err != nil || count != 1 {
			t.Fatal("Enumeration included an unauthorized user", count, err)
		}
		p, err := parsePresence(list[1])
		if err != nil || p.name != localPresenceName(host) || p.state != state {
			t.Fatalf("Incorrect friend presence: %+v, %v", p, err)
		}
		cursor, err := base64.StdEncoding.DecodeString(string(list[2]))
		if err != nil || !bytes.Equal(cursor, mustPresenceCursorTest(host)) {
			t.Fatal("Cursor does not match the enumerated set")
		}
	}
	read(localPresenceOffline)
	done, err := readGRPCStreamFrame(response.Body)
	if err != nil || !bytes.Equal(done, protoBytes(nil, 2, nil)) {
		t.Fatal("Incorrect initial enumeration", err)
	}
	lease := a.presences.open(host)
	read(localPresenceOnline)
	a.presences.close(host, lease)
	read(localPresenceOffline)
}

func mustPresenceCursorTest(uid string) []byte {
	return protoBytes(nil, 1, protoBytes(protoBytes(nil, 1, []byte(uid)), 2, nil))
}
