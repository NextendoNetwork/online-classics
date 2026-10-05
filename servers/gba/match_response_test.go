package main

import (
	"bytes"
	"encoding/hex"
	"log"
	"strings"
	"testing"
)

func TestGenesisQueryMembersExperimentThroughHTTP2(t *testing.T) {
	s := newTicketStore()
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	hostUID, _ := matchIdentity(t, a, "host")
	_, guestToken := matchIdentity(t, a, "guest")
	host, _ := publishTestHost(t, s, hostUID, "query-view", "private-password")
	props := decodeTestMessage(t, testDocBytes("filter", fld("consoleName", tvStr("MD"))))[2]
	s.sessions[host.sessionName].host.request.properties = props
	server := matchTestServer(t, s, a)
	q := protoBytes(nil, 1, []byte("tenants/current"))
	q = protoVarint(q, 2, 1)
	q = protoBytes(q, 3, []byte("tenants/current/gameSessionSearchConfigs/LCLA6"))
	q = protoBytes(q, 7, []byte("tenants/current/users/"+hostUID))
	call := func() map[uint64][]byte {
		t.Helper()
		answer := callLabGRPC(t, server.Client(), server.URL+queryGameSessionsPath, q, guestToken)
		if answer.status != "0" || bytes.Contains(answer.body, []byte("private-password")) {
			t.Fatal("Invalid response or exposed password")
		}
		gs := decodeTestMessage(t, decodeTestMessage(t, answer.body)[1])
		if string(gs[1]) != host.sessionName || !bytes.Equal(gs[11], props) || !bytes.Equal(gs[4], []byte{1}) || !bytes.Equal(gs[7], []byte{2}) {
			t.Fatal("Experiment changed the room or its properties")
		}
		return gs
	}
	if call()[12] != nil {
		t.Fatal("Default BASIC included participants")
	}
	s.mu.Lock()
	s.genesisQueryMembers = true
	s.mu.Unlock()
	member := decodeTestMessage(t, call()[12])
	if string(member[1]) != host.userName || string(member[2]) != "tenants/"+labTenant+"/users/"+hostUID {
		t.Fatal("Experiment did not include the actual host")
	}
}

func TestQueryMembersExperimentScope(t *testing.T) {
	s := newTicketStore()
	s.genesisQueryMembers = true
	for _, tc := range []struct {
		search, creation string
		view, want       uint64
	}{
		{"LCLA6", "LCLA6-2P", 1, 2},
		{"LCLA6", "LCLA6-2P", 2, 2},
		{"LCLA6", "LCLA6-2P", 3, 3},
		{"", "LCLA6-2P", 1, 1},
		{"N64", "N64", 1, 1},
		{"LCLA6", "LCLA6-4P", 1, 2},
		{"LCLA6", "LCLA6-8P", 1, 1},
	} {
		got := s.matchQueryView(matchQuery{config: tc.search, view: tc.view}, &matchSession{host: pendingTicket{config: tc.creation}})
		if got != tc.want {
			t.Fatalf("%+v: view=%d", tc, got)
		}
	}
}

func TestHostNSADiagnosticChecksByteOrderWithoutPublishingIDs(t *testing.T) {
	const nsa = "0123456789abcdef"
	expected, _ := hex.DecodeString(nsa)
	for _, order := range []string{"big", "little", "unrelated"} {
		hostID := append([]byte(nil), expected...)
		if order == "little" {
			for i := 0; i < 4; i++ {
				hostID[i], hostID[7-i] = hostID[7-i], hostID[i]
			}
		}
		if order == "unrelated" {
			hostID[0] ^= 255
		}
		props := decodeTestMessage(t, testDocBytes("filter", fld("hostNsaId", protoBytes(nil, 8, hostID))))[2]
		session := &matchSession{host: pendingTicket{owner: "private-owner", request: requestedGameSession{properties: props}}, members: map[string]pendingTicket{"private-owner": {}}}
		pair := &localFriendPair{peers: map[string]localFriendPeer{"client": {uid: "private-owner", nsa: nsa}}}
		var out bytes.Buffer
		logMatchResponseStructure(log.New(&out, "", 0), pair, session, 1, 2)
		got := out.String()
		wantBig, wantLittle := "false", "false"
		if order == "big" {
			wantBig = "true"
		}
		if order == "little" {
			wantLittle = "true"
		}
		if !strings.Contains(got, "host_nsa_matches_big="+wantBig+" host_nsa_matches_little="+wantLittle) || !strings.Contains(got, "host_nsa_bytes=8") {
			t.Fatal("Byte order not diagnosed", got)
		}
		if strings.Contains(got, nsa) || strings.Contains(got, hex.EncodeToString(hostID)) || strings.Contains(got, "private-owner") {
			t.Fatal("Diagnostic published identity")
		}
	}
}

func TestGBAQueryMembersExperimentThroughHTTP2(t *testing.T) {
	s := newTicketStore()
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	hostUID, _ := matchIdentity(t, a, "host")
	_, guestToken := matchIdentity(t, a, "guest")
	host, _ := publishTestHost(t, s, hostUID, "query-view", "private-password")
	props := decodeTestMessage(t, testDocBytes("filter", fld("consoleName", tvStr("MD"))))[2]
	s.sessions[host.sessionName].host.request.properties = props
	s.sessions[host.sessionName].host.config = "tenants/current/matchmakingConfigs/LCLA6-4P"
	server := matchTestServer(t, s, a)
	q := protoBytes(nil, 1, []byte("tenants/current"))
	q = protoVarint(q, 2, 1)
	q = protoBytes(q, 3, []byte("tenants/current/gameSessionSearchConfigs/LCLA6"))
	q = protoBytes(q, 7, []byte("tenants/current/users/"+hostUID))
	call := func() map[uint64][]byte {
		t.Helper()
		answer := callLabGRPC(t, server.Client(), server.URL+queryGameSessionsPath, q, guestToken)
		if answer.status != "0" || bytes.Contains(answer.body, []byte("private-password")) {
			t.Fatal("Invalid response or exposed password")
		}
		gs := decodeTestMessage(t, decodeTestMessage(t, answer.body)[1])
		if string(gs[1]) != host.sessionName || !bytes.Equal(gs[11], props) || !bytes.Equal(gs[4], []byte{1}) || !bytes.Equal(gs[7], []byte{2}) {
			t.Fatal("Experiment changed the room or its properties")
		}
		return gs
	}
	if call()[12] != nil {
		t.Fatal("Default BASIC included participants")
	}
	s.mu.Lock()
	s.genesisQueryMembers = true
	s.mu.Unlock()
	member := decodeTestMessage(t, call()[12])
	if string(member[1]) != host.userName || string(member[2]) != "tenants/"+labTenant+"/users/"+hostUID {
		t.Fatal("Experiment did not include the actual host")
	}
}
