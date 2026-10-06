package main

import (
	"bytes"
	"testing"
)

func TestN64FourPlayerCreationIsDiscoverable(t *testing.T) {
	store := newTicketStore()
	auth, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	hostUID, _ := matchIdentity(t, auth, "n64-host")
	_, guestToken := matchIdentity(t, auth, "n64-guest")
	host, _ := publishTestHost(t, store, hostUID, "n64-search", "")
	store.mu.Lock()
	session := store.sessions[host.sessionName]
	session.host.config = "tenants/" + labTenant + "/gameSessionCreationConfigs/LCLA6-4P"
	session.host.request.capacity = 0
	session.host.request.properties = decodeTestMessage(t, testDocBytes("filter", fld("consoleName", tvStr("N64")), fld("applicationVersion", tvStr("4.2.0"))))[2]
	store.mu.Unlock()
	server := matchTestServer(t, store, auth)
	query := protoBytes(nil, 1, []byte("tenants/current"))
	query = protoBytes(query, 3, []byte("tenants/current/gameSessionSearchConfigs/LCLA6"))
	query = protoVarint(query, 4, 1)
	query = protoBytes(query, 7, []byte("tenants/"+labTenant+"/users/"+hostUID))
	query = protoBytes(query, 6, decodeTestMessage(t, testDocBytes("filter", fld("ConsoleName", tvStr("N64")), fld("ApplicationVersion", tvStr("4.2.0"))))[2])
	answer := callLabGRPC(t, server.Client(), server.URL+queryGameSessionsPath, query, guestToken)
	if answer.status != "0" || !bytes.Contains(answer.body, []byte(host.sessionName)) {
		t.Fatalf("N64 room not discoverable: status=%s", answer.status)
	}
	if capacity := sessionCapacity(session.host); capacity != 4 {
		t.Fatalf("capacity=%d, want 4", capacity)
	}
	wrongVersion := protoBytes(query, 6, decodeTestMessage(t, testDocBytes("filter", fld("ApplicationVersion", tvStr("1.0.0"))))[2])
	wrong := callLabGRPC(t, server.Client(), server.URL+queryGameSessionsPath, wrongVersion, guestToken)
	if wrong.status != "0" || bytes.Contains(wrong.body, []byte(host.sessionName)) {
		t.Fatal("Incompatible N64 version passes alias filtering")
	}
	store.leaveMatchSession(host.sessionName, resourceLeaf(host.userName))
	answer = callLabGRPC(t, server.Client(), server.URL+queryGameSessionsPath, query, guestToken)
	if answer.status != "0" || bytes.Contains(answer.body, []byte(host.sessionName)) {
		t.Fatal("Closed N64 room remains discoverable")
	}
}
