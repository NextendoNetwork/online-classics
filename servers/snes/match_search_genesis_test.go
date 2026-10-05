package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestGenesisSearchConfigFindsTwoPlayerCreationWithFilters(t *testing.T) {
	s := newTicketStore()
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	hostUID, _ := matchIdentity(t, a, "genesis-host")
	guestUID, guestToken := matchIdentity(t, a, "genesis-guest")
	host, _ := publishTestHost(t, s, hostUID, "genesis-search", "")
	properties := decodeTestMessage(t, testDocBytes("filter", fld("version", tvInt(1)), fld("mode", tvStr("online"))))[2]
	s.mu.Lock()
	s.sessions[host.sessionName].host.request.properties = properties
	s.mu.Unlock()
	server := matchTestServer(t, s, a)
	q := protoBytes(nil, 1, []byte("tenants/current"))
	q = protoBytes(q, 3, []byte("tenants/current/gameSessionSearchConfigs/LCLA6"))
	q = protoVarint(q, 4, 1)
	q = protoBytes(q, 6, properties)
	call := func(b []byte) labAnswer {
		return callLabGRPC(t, server.Client(), server.URL+queryGameSessionsPath, b, guestToken)
	}
	good := call(protoBytes(q, 7, []byte("tenants/"+labTenant+"/users/"+hostUID)))
	if good.status != "0" || !bytes.Contains(good.body, []byte(host.sessionName)) {
		t.Fatalf("LCLA6 cannot find LCLA6-2P: status=%s", good.status)
	}
	unknown := call(protoBytes(q, 7, []byte("tenants/"+labTenant+"/users/"+guestUID)))
	if unknown.status != "0" || len(unknown.body) != 0 {
		t.Fatal("User filter omitted")
	}
	wrongProps := call(protoBytes(q, 6, decodeTestMessage(t, testDocBytes("filter", fld("mode", tvStr("different"))))[2]))
	if wrongProps.status != "0" || len(wrongProps.body) != 0 {
		t.Fatal("Property filter omitted")
	}
	for _, config := range []string{"LCLA6-4P", "LCLA6-extra", "N64", ""} {
		if config != "" && matchSearchConfig("LCLA6", config) {
			t.Fatalf("Accepts unrelated configuration: %s", config)
		}
	}
}

func TestMatchPropertyDiagnosticsIdentifyMismatchWithoutValues(t *testing.T) {
	secretKey := "private-key-example"
	secretActual := "private-room-value"
	secretExpected := "private-query-value"
	properties := decodeTestMessage(t, testDocBytes("filter", fld(secretKey, tvStr(secretActual))))[2]
	session := &matchSession{host: pendingTicket{request: requestedGameSession{properties: properties}}}
	q := matchQuery{properties: []gsField{fld(secretKey, tvStr(secretExpected)), fld("absent-property", tvInt(1))}}
	var out bytes.Buffer
	logMatchPropertyComparison(log.New(&out, "", 0), q, session)
	got := out.String()
	for _, secret := range []string{secretKey, secretActual, secretExpected, "absent-property"} {
		if strings.Contains(got, secret) {
			t.Fatal("Diagnostic published a key or value")
		}
	}
	for _, expected := range []string{"room_parse_valid=true room_properties=1 query_properties=2", "exists_in_room=true query_type=7 room_type=7", "exists_in_room=false query_type=3 room_type=0", "encoded_value_equal=false"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("Missing diagnostic %q: %s", expected, got)
		}
	}
}

func TestMatchPropertyDiagnosticsDetectDifferentKeyForSameValue(t *testing.T) {
	properties := decodeTestMessage(t, testDocBytes("filter", fld("application_version", tvStr("3.1.1")), fld("private-unknown-key", tvStr("MD"))))[2]
	session := &matchSession{host: pendingTicket{request: requestedGameSession{properties: properties}}}
	q := matchQuery{properties: []gsField{fld("ApplicationVersion", tvStr("3.1.1"))}}
	var out bytes.Buffer
	logMatchPropertyComparison(log.New(&out, "", 0), q, session)
	got := out.String()
	if !strings.Contains(got, "same_key=false same_key_case_insensitive=false same_encoded_value=true") || !strings.Contains(got, "key_schema=\"ApplicationVersion\"") {
		t.Fatal("Did not identify mapping between distinct keys")
	}
	if strings.Contains(got, "3.1.1") || strings.Contains(got, "private-unknown-key") || strings.Contains(got, "\"MD\"") {
		t.Fatal("Diagnostic published unknown values or keys")
	}
}

func TestGenesisObservedPropertyAliasesThroughHTTP2(t *testing.T) {
	s := newTicketStore()
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	hostUID, _ := matchIdentity(t, a, "genesis-host")
	_, guestToken := matchIdentity(t, a, "genesis-guest")
	host, _ := publishTestHost(t, s, hostUID, "genesis-alias", "")
	server := matchTestServer(t, s, a)
	query := func(console, version string) []byte {
		q := protoBytes(nil, 1, []byte("tenants/current"))
		q = protoBytes(q, 3, []byte("tenants/current/gameSessionSearchConfigs/LCLA6"))
		q = protoVarint(q, 4, 1)
		q = protoBytes(q, 6, decodeTestMessage(t, testDocBytes("filter", fld("ConsoleName", tvStr(console)), fld("ApplicationVersion", tvStr(version))))[2])
		return protoBytes(q, 7, []byte("tenants/"+labTenant+"/users/"+hostUID))
	}
	setProps := func(fields ...gsField) {
		s.mu.Lock()
		s.sessions[host.sessionName].host.request.properties = decodeTestMessage(t, testDocBytes("filter", fields...))[2]
		s.mu.Unlock()
	}
	setProps(fld("consoleName", tvStr("MD")), fld("applicationVersion", tvStr("3.1.1")))
	call := func(q []byte, found bool) {
		t.Helper()
		answer := callLabGRPC(t, server.Client(), server.URL+queryGameSessionsPath, q, guestToken)
		if answer.status != "0" || bytes.Contains(answer.body, []byte(host.sessionName)) != found {
			t.Fatalf("Room found=%t expected=%t status=%s", bytes.Contains(answer.body, []byte(host.sessionName)), found, answer.status)
		}
	}
	call(query("MD", "3.1.1"), true)
	call(query("N64", "3.1.1"), false)
	call(query("MD", "3.1.0"), false)
	setProps(fld("consoleName", tvStr("MD")))
	call(query("MD", "3.1.1"), false)
	setProps(fld("consoleName", tvStr("MD")), fld("ConsoleName", tvStr("N64")), fld("applicationVersion", tvStr("3.1.1")))
	call(query("MD", "3.1.1"), false)
	call(query("N64", "3.1.1"), true) // The exact key wins even if the alias differs.
	setProps(fld("ConsoleName", tvStr("MD")), fld("ApplicationVersion", tvStr("3.1.1")))
	call(query("MD", "3.1.1"), true)
	setProps(fld("consoleName", tvStr("MD")), fld("applicationVersion", tvStr("3.1.1")))
	s.mu.Lock()
	s.sessions[host.sessionName].closed = true
	s.mu.Unlock()
	call(query("MD", "3.1.1"), false)
}

func TestGenesisPropertyAliasesAreRestrictedToObservedNamesAndConfigs(t *testing.T) {
	props := []gsField{fld("consoleName", tvStr("MD")), fld("applicationVersion", tvStr("3.1.1")), fld("mode", tvStr("online"))}
	for _, tc := range []struct {
		search, creation, key string
		found                 bool
	}{
		{"LCLA6", "LCLA6-2P", "ConsoleName", true},
		{"LCLA6", "LCLA6-2P", "ApplicationVersion", true},
		{"LCLA6", "LCLA6-2P", "Mode", false},
		{"LCLA6", "LCLA6-2P", "CONSOLENAME", false},
		{"N64", "N64", "ConsoleName", false},
		{"LCLA6", "LCLA6-4P", "ConsoleName", false},
		{"", "LCLA6-2P", "ConsoleName", false},
		{"LCLA6-2P", "LCLA6-2P", "ConsoleName", false},
		{"N64", "N64", "mode", true},
	} {
		q := matchQuery{config: tc.search}
		session := &matchSession{host: pendingTicket{config: tc.creation}}
		_, found := q.propertyValue(session, props, tc.key)
		if found != tc.found {
			t.Fatalf("%+v: found=%t", tc, found)
		}
	}
}
