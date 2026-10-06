package main

import (
	"bytes"
	"crypto/sha256"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func matchIdentity(t *testing.T, a *labAuth, label string) (string, string) {
	t.Helper()
	b, err := a.makeResponse(authRequest{tenant: "tenants/" + labTenant, externalToken: label})
	if err != nil {
		t.Fatal(err)
	}
	m := decodeTestMessage(t, b)
	user := decodeTestMessage(t, m[1])
	token := decodeTestMessage(t, m[2])
	return resourceLeaf(string(user[1])), string(token[2])
}

func matchTestServer(t *testing.T, s *ticketStore, a *labAuth) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(queryGameSessionsPath, s.queryMatchSessions(a, quiet(), newRoomStore()))
	mux.HandleFunc(joinGameSessionPath, s.joinMatchSession(a, quiet()))
	mux.HandleFunc(getGameSessionPath, s.getMatchSession(a, quiet()))
	mux.HandleFunc(gamesyncIssueTokenPath, s.issueGamesyncToken(a, quiet()))
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func publishTestHost(t *testing.T, s *ticketStore, uid, id, password string) (pendingTicket, *gamesyncChannel) {
	t.Helper()
	name := "tenants/" + labTenant + "/gameSessions/" + id
	host := pendingTicket{owner: uid, sessionName: name, userName: name + "/userSessions/host", createdAt: time.Now(), config: "tenants/current/matchmakingConfigs/LCLA6-2P", user: protoBytes(nil, 1, []byte("tenants/"+labTenant+"/users/"+uid)), request: requestedGameSession{capacity: 2, isPublic: true, password: password}}
	if err := s.publishMatchSession(host); err != nil {
		t.Fatal(err)
	}
	room := s.gamesyncRoom(host)
	ch := room.openChannel(gamesyncActorFor(host))
	t.Cleanup(func() { room.closeChannel(ch, quiet()) })
	return host, ch
}

func joinTestPayload(name, password, uid string) []byte {
	b := protoBytes(nil, 1, []byte(name))
	b = protoBytes(b, 2, []byte(password))
	return protoBytes(b, 3, protoBytes(nil, 1, []byte("tenants/"+labTenant+"/users/"+uid)))
}

func TestRealRoomJoinIsolationCapacityAndDeparture(t *testing.T) {
	s := newTicketStore()
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	a.sessionProfile = "npln-gss"
	hostUID, _ := matchIdentity(t, a, "host")
	guestUID, guestToken := matchIdentity(t, a, "guest")
	thirdUID, thirdToken := matchIdentity(t, a, "third")
	host, hostChannel := publishTestHost(t, s, hostUID, "room-one", "private-password")
	server := matchTestServer(t, s, a)
	call := func(path string, b []byte, token string) labAnswer {
		return callLabGRPC(t, server.Client(), server.URL+path, b, token)
	}
	query := protoBytes(nil, 1, []byte("tenants/current"))
	listed := call(queryGameSessionsPath, query, guestToken)
	if listed.status != "0" || !bytes.Contains(listed.body, []byte(host.sessionName)) || bytes.Contains(listed.body, []byte("private-password")) {
		t.Fatal("Actual room not listed or password exposed")
	}
	if got := call(joinGameSessionPath, joinTestPayload(host.sessionName, "bad", guestUID), guestToken); got.status != "7" {
		t.Fatalf("Password: %s", got.status)
	}
	if got := call(joinGameSessionPath, joinTestPayload(host.sessionName, "private-password", hostUID), guestToken); got.status != "7" {
		t.Fatalf("Impersonation: %s", got.status)
	}
	joined := call(joinGameSessionPath, joinTestPayload(host.sessionName, "private-password", guestUID), guestToken)
	if joined.status != "0" {
		t.Fatalf("Join: %s", joined.status)
	}
	matched := decodeTestMessage(t, decodeTestMessage(t, joined.body)[1])
	guestName, matchToken := string(matched[2]), string(matched[3])
	if guestName == host.userName || !strings.HasPrefix(guestName, host.sessionName+"/userSessions/") {
		t.Fatal("J2 does not have its own session in the same room")
	}
	if got := call(joinGameSessionPath, joinTestPayload(host.sessionName, "private-password", thirdUID), thirdToken); got.status != "8" {
		t.Fatalf("Capacity: %s", got.status)
	}
	retry := call(joinGameSessionPath, joinTestPayload(host.sessionName, "private-password", guestUID), guestToken)
	if string(decodeTestMessage(t, decodeTestMessage(t, retry.body)[1])[2]) != guestName {
		t.Fatal("Retry duplicated player")
	}
	if got := call(gamesyncIssueTokenPath, protoBytes(protoBytes(nil, 1, []byte(host.userName)), 2, []byte(matchToken)), ""); got.status != "16" {
		t.Fatalf("J2 token accepted for host: %s", got.status)
	}
	issued := call(gamesyncIssueTokenPath, protoBytes(protoBytes(nil, 1, []byte(guestName)), 2, []byte(matchToken)), "")
	if issued.status != "0" {
		t.Fatalf("Gamesync J2: %s", issued.status)
	}
	access := string(decodeTestMessage(t, decodeTestMessage(t, issued.body)[1])[2])
	s.mu.Lock()
	guest := s.issuedGamesync[sha256.Sum256([]byte(access))].ticket
	s.mu.Unlock()
	room := s.gamesyncRoom(guest)
	guestChannel := room.openChannel(gamesyncActorFor(guest))
	// Two channels receive the same opaque J2 write without accepting host writes
	// to another participant's document or reads from another room.
	guestID := resourceLeaf(guestName)
	path := "docs/__pgn/All/__stu/" + guestID
	for _, ch := range []*gamesyncChannel{hostChannel, guestChannel} {
		actor := gamesyncActorFor(host)
		if ch == guestChannel {
			actor = gamesyncActorFor(guest)
		}
		room.installWatch(ch, actor, gamesyncTarget{name: "userSessions/current/targets/1", kind: 2, paths: []string{path}}, quiet())
		ch.q.next()
	}
	if _, _, err := room.applyWrites(gamesyncActorFor(guest), []gsWrite{{kind: 1, path: path, mask: []string{"*"}, fields: []gsField{fld("payload", tvStr("peer-state"))}}}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []*gamesyncChannel{hostChannel, guestChannel} {
		frames, _ := ch.q.next()
		if len(frames) != 1 || !bytes.Contains(frames[0], []byte("peer-state")) {
			t.Fatal("Change not shared between players")
		}
	}
	if _, _, err := room.applyWrites(gamesyncActorFor(host), []gsWrite{{kind: 4, path: path}}, nil, time.Now()); err == nil {
		t.Fatal("Host wrote to another participant's document")
	}
	room.closeChannel(guestChannel, quiet())
	s.mu.Lock()
	count := len(s.sessions[host.sessionName].members)
	_, valid := s.issuedGamesync[sha256.Sum256([]byte(access))]
	s.mu.Unlock()
	if count != 1 || valid {
		t.Fatal("Departure does not release slot or revoke token")
	}
	if got := call(joinGameSessionPath, joinTestPayload(host.sessionName, "private-password", thirdUID), thirdToken); got.status != "0" {
		t.Fatalf("Slot released: %s", got.status)
	}
	s.leaveMatchSession(host.sessionName, resourceLeaf(host.userName))
	if got := call(queryGameSessionsPath, query, guestToken); len(got.body) != 0 {
		t.Fatal("Closed room remains visible")
	}
	if got := call(joinGameSessionPath, joinTestPayload(host.sessionName, "private-password", guestUID), guestToken); got.status != "5" {
		t.Fatal("Closed room accepts participants")
	}
}

func TestRoomQueryCursorBoundToUserAndFilter(t *testing.T) {
	s := newTicketStore()
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	uid, token := matchIdentity(t, a, "host")
	_, other := matchIdentity(t, a, "other")
	publishTestHost(t, s, uid, "room-a", "")
	publishTestHost(t, s, uid, "room-b", "")
	server := matchTestServer(t, s, a)
	call := func(b []byte, token string) labAnswer {
		return callLabGRPC(t, server.Client(), server.URL+queryGameSessionsPath, b, token)
	}
	q := protoVarint(protoBytes(nil, 1, []byte("tenants/current")), 8, 1)
	first := call(q, token)
	cursor := decodeTestMessage(t, first.body)[2]
	if first.status != "0" || len(cursor) == 0 {
		t.Fatal("Missing next page")
	}
	second := call(protoBytes(q, 9, cursor), token)
	if second.status != "0" || bytes.Equal(first.body, second.body) || len(decodeTestMessage(t, second.body)[2]) > 0 {
		t.Fatal("Incorrect pagination")
	}
	if got := call(protoBytes(q, 9, cursor), other); got.status != "3" {
		t.Fatal("Unauthorized cursor accepted")
	}
	if got := call(protoVarint(protoBytes(q, 9, cursor), 4, 2), token); got.status != "3" {
		t.Fatal("Cursor accepted with changed filter")
	}
	if got := call(protoBytes(q, 3, []byte("tenants/current/matchmakingConfigs/OTHER")), token); got.status != "0" || len(got.body) != 0 {
		t.Fatal("Game configurations were mixed")
	}
}

func TestLANPeerPolicyAndListenerValidation(t *testing.T) {
	_, subnet, err := net.ParseCIDR("192.168.50.0/24")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ip   string
		want bool
	}{{"192.168.50.9", true}, {"127.0.0.1", true}, {"192.168.51.9", false}, {"8.8.8.8", false}} {
		if got := labPeerAllowed(subnet, net.ParseIP(tc.ip)); got != tc.want {
			t.Fatalf("peer %s: %t", tc.ip, got)
		}
	}
	for _, ip := range []string{"0.0.0.0", "8.8.8.8"} {
		if _, err := labListenerNetwork(net.ParseIP(ip)); err == nil {
			t.Fatalf("Invalid destination accepted: %s", ip)
		}
	}
}

func TestLANListenersAdvertiseOwnAdapter(t *testing.T) {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range addresses {
		subnet, ok := address.(*net.IPNet)
		if !ok || subnet.IP.To4() == nil || !subnet.IP.IsPrivate() || subnet.IP.IsLoopback() {
			continue
		}
		ip := subnet.IP.String()
		stun, advertised, err := startLocalSTUN(net.JoinHostPort(ip, "0"), quiet())
		if err != nil {
			t.Fatal(err)
		}
		defer stun.Close()
		relay, err := startLocalTURN(net.JoinHostPort(ip, "0"), quiet())
		if err != nil {
			t.Fatal(err)
		}
		defer relay.Close()
		if advertised.host != ip || relay.endpoint.host != ip || advertised.port == 0 || relay.endpoint.port == 0 {
			t.Fatal("LAN listeners announce incorrect destination")
		}
		return
	}
	t.Skip("No assigned private IPv4 for LAN listener tests")
}

func TestAbandonedJoinReservationExpiresWithoutEvictingActiveGuest(t *testing.T) {
	s := newTicketStore()
	host, _ := publishTestHost(t, s, "host", "expiry", "")
	guest := host
	guest.owner = "guest"
	guest.userName = host.sessionName + "/userSessions/guest"
	guest.createdAt = time.Now().Add(-6 * time.Minute)
	s.mu.Lock()
	session := s.sessions[host.sessionName]
	session.members[guest.owner] = guest
	s.mu.Unlock()
	room := s.gamesyncRoom(guest)
	ch := room.openChannel(gamesyncActorFor(guest))
	s.mu.Lock()
	s.expireJoinReservations(session, time.Now())
	count := len(session.members)
	s.mu.Unlock()
	if count != 2 {
		t.Fatal("Participant expired with active Gamesync")
	}
	room.closeChannel(ch, quiet())
	s.mu.Lock()
	session.members[guest.owner] = guest
	s.expireJoinReservations(session, time.Now())
	count = len(session.members)
	s.mu.Unlock()
	if count != 1 {
		t.Fatal("Abandoned reservation did not expire")
	}
}
