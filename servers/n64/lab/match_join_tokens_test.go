package main

import (
	"bytes"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJoinRequestedSelfTokenAndDelegationValidation(t *testing.T) {
	s := newTicketStore()
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	a.sessionProfile = "npln-gss"
	hostUID, _ := matchIdentity(t, a, "token-host")
	guestUID, guestToken := matchIdentity(t, a, "token-guest")
	host, _ := publishTestHost(t, s, hostUID, "requested-token-room", "")
	var logs bytes.Buffer
	server := httptest.NewUnstartedServer(s.joinMatchSession(a, log.New(&logs, "", 0)))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	base := joinTestPayload(host.sessionName, "", guestUID)
	base = base[:len(base):len(base)] // Each appended request owns its buffer.
	call := func(b []byte) labAnswer {
		return callLabGRPC(t, server.Client(), server.URL+joinGameSessionPath, b, guestToken)
	}
	for _, tc := range []struct {
		name    string
		payload []byte
		status  string
	}{
		{"foreign host token", protoBytes(base, 5, []byte("tenants/"+labTenant+"/users/"+hostUID)), "7"},
		{"empty requested user", protoBytes(base, 5, nil), "7"},
		{"wrong tenant", protoBytes(base, 5, []byte("tenants/foreign/users/"+guestUID)), "7"},
		{"delegation credential", protoBytes(base, 4, []byte("secret-delegation-sentinel")), "12"},
		{"malformed optional field", protoVarint(base, 5, 1), "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := call(tc.payload); got.status != tc.status {
				t.Fatalf("status = %s, want %s", got.status, tc.status)
			}
			s.mu.Lock()
			count := len(s.sessions[host.sessionName].members)
			s.mu.Unlock()
			if count != 1 {
				t.Fatal("rejected request reserved a seat")
			}
		})
	}
	var guestName string
	for _, user := range []string{"tenants/current/users/current", "tenants/current/users/" + guestUID, "tenants/" + labTenant + "/users/" + guestUID} {
		// Empty delegation is not a credential; repeated self requests still
		// produce one participant and one token for that participant.
		payload := protoBytes(protoBytes(protoBytes(base, 4, nil), 5, []byte(user)), 5, []byte(user))
		got := call(payload)
		if got.status != "0" {
			t.Fatalf("self token join: %s", got.status)
		}
		matched := decodeTestMessage(t, decodeTestMessage(t, got.body)[1])
		definition := decodeTestMessage(t, matched[1])
		if string(definition[1]) != "tenants/"+labTenant+"/users/"+guestUID || len(matched[3]) == 0 {
			t.Fatal("missing self identity or matchmaking token")
		}
		if guestName != "" && guestName != string(matched[2]) {
			t.Fatal("retry duplicated participant")
		}
		guestName = string(matched[2])
		// Validate the issued token through the actual Gamesync handler.
		gs := matchTestServer(t, s, a)
		issued := callLabGRPC(t, gs.Client(), gs.URL+gamesyncIssueTokenPath, protoBytes(protoBytes(nil, 1, matched[2]), 2, matched[3]), "")
		gs.Close()
		if issued.status != "0" {
			t.Fatalf("J2 token rejected by Gamesync: %s", issued.status)
		}
	}
	if strings.Contains(logs.String(), "secret-delegation-sentinel") || strings.Contains(logs.String(), guestToken) || strings.Contains(logs.String(), guestUID) {
		t.Fatal("credentials or user identifiers leaked into diagnostics")
	}
	if !strings.Contains(logs.String(), "own_tokens=2") || !strings.Contains(logs.String(), "delegations=1") {
		t.Fatal("missing structural diagnostics")
	}
}
