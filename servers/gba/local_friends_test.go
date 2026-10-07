package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLocalFriendLoopbackDestinationsUseSocketAddress(t *testing.T) {
	pair, err := newLocalFriendPair("127.0.0.1,127.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if err := pair.setNSAOverrides("127.0.0.1=0102030405060708,127.0.0.2=1112131415161718"); err != nil {
		t.Fatal(err)
	}
	var identities []string
	for _, destination := range []string{"127.0.0.1", "127.0.0.2"} {
		listener, err := net.Listen("tcp4", net.JoinHostPort(destination, "0"))
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, peer, ok := pair.peerFor(r)
			if !ok || key != destination {
				http.Error(w, "Incorrect destination identity", 500)
				return
			}
			io.WriteString(w, peer.uid)
		}))
		server.Listener.Close()
		server.Listener = listener
		server.EnableHTTP2 = true
		server.StartTLS()
		// The httptest certificate covers localhost; verify that name even when
		// the test socket uses the second loopback destination.
		transport := server.Client().Transport.(*http.Transport)
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
		transport.TLSClientConfig.ServerName = "127.0.0.1"
		request, err := http.NewRequest("GET", server.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Host = "127.0.0.99"
		request.Header.Set("X-Forwarded-For", "127.0.0.99")
		response, err := server.Client().Do(request)
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		server.Close()
		if err != nil || response.StatusCode != 200 || response.ProtoMajor != 2 {
			t.Fatal("Did not identify the actual HTTP/2 destination", err)
		}
		identities = append(identities, string(data))
	}
	if identities[0] == identities[1] {
		t.Fatal("Merged the two emulator identities")
	}
	r := httptest.NewRequest("GET", "https://127.0.0.2", nil)
	r.RemoteAddr = "10.77.20.92:45000"
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.2"), Port: 443}))
	if _, _, ok := pair.peerFor(r); ok {
		t.Fatal("LAN client adopted a loopback emulator identity")
	}
}

func TestLocalFriendNSAOverridesAreAtomicAndKeepUserIdentities(t *testing.T) {
	p, err := newLocalFriendPair("127.0.0.1,10.77.20.92")
	if err != nil {
		t.Fatal(err)
	}
	uid := p.peers["127.0.0.1"].uid
	if err := p.setNSAOverrides("127.0.0.1=0102030405060708,10.77.20.92=1112131415161718"); err != nil {
		t.Fatal(err)
	}
	if p.peers["127.0.0.1"].uid != uid || p.peers["10.77.20.92"].nsa != "1112131415161718" {
		t.Fatal("Mapping changed UID or lost NSA")
	}
	for _, config := range []string{"127.0.0.1=0102030405060708", "127.0.0.1=ffffffffffffffff,10.77.20.93=1112131415161718", "127.0.0.1=0000000000000000,10.77.20.92=1112131415161718", "127.0.0.1=1112131415161718,10.77.20.92=1112131415161718"} {
		if p.setNSAOverrides(config) == nil {
			t.Fatal("Accepted invalid mapping")
		}
		if p.peers["127.0.0.1"].nsa != "0102030405060708" {
			t.Fatal("Failure left a partially changed mapping")
		}
	}
	var disabled *localFriendPair
	if disabled.setNSAOverrides("127.0.0.1=0102030405060708,10.77.20.92=1112131415161718") == nil {
		t.Fatal("Accepted IDs without a pair")
	}
}

func authenticatePairTest(t *testing.T, a *labAuth, ip, external string) (string, string) {
	t.Helper()
	b := protoBytes(nil, 1, []byte("tenants/"+labTenant))
	b = protoBytes(b, 3, protoBytes(nil, 2, []byte(external)))
	r := httptest.NewRequest("POST", issuePrearrangedUserTokenPath, bytes.NewReader(streamTestFrame(b)))
	r.RemoteAddr = ip + ":55000"
	r.Header.Set("Content-Type", "application/grpc")
	r.Header.Set("npln-tenant-id", labTenant)
	w := httptest.NewRecorder()
	a.issuePrearrangedUserToken(w, r)
	if w.Result().Trailer.Get("Grpc-Status") != "0" {
		t.Fatal("Pair authentication rejected")
	}
	m := decodeTestMessage(t, w.Body.Bytes()[5:])
	return resourceLeaf(string(decodeTestMessage(t, m[1])[1])), string(decodeTestMessage(t, m[2])[2])
}

func TestLocalFriendSubscriptionFeedsRealRoomSearch(t *testing.T) {
	pair, err := newLocalFriendPair("127.0.0.1,10.77.20.92")
	if err != nil {
		t.Fatal(err)
	}
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	a.friendPair = pair
	hostUID, hostToken := authenticatePairTest(t, a, "127.0.0.1", "host-token-1")
	reauth, _ := authenticatePairTest(t, a, "127.0.0.1", "host-token-2")
	if reauth != hostUID {
		t.Fatal("External token renewal changed client identity")
	}
	s := newTicketStore()
	host, _ := publishTestHost(t, s, hostUID, "paired-room", "")
	mux := http.NewServeMux()
	mux.HandleFunc(subscribeFriendsPath, a.subscribeFriends)
	mux.HandleFunc(listFriendUsersPath, a.listLocalFriends)
	mux.HandleFunc(queryGameSessionsPath, s.queryMatchSessions(a, quiet(), newRoomStore()))
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, "POST", server.URL+subscribeFriendsPath, bytes.NewReader(streamTestFrame(protoBytes(nil, 1, []byte("tenants/current/users/current")))))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/grpc")
	r.Header.Set("npln-tenant-id", labTenant)
	r.Header.Set("Authorization", "Bearer "+hostToken)
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	first, err := readGRPCStreamFrame(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(decodeTestMessage(t, first)[1]) != 0 {
		t.Fatal("Published friend before authenticating the second client")
	}
	guestUID, guestToken := authenticatePairTest(t, a, "10.77.20.92", "guest-token")
	_, _ = authenticatePairTest(t, a, "10.77.20.93", "third-token")
	update, err := readGRPCStreamFrame(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	account := decodeTestMessage(t, decodeTestMessage(t, update)[1])
	friend := decodeTestMessage(t, account[2])
	if string(friend[2]) != "tenants/"+labTenant+"/users/"+guestUID || len(account[1]) != 16 {
		t.Fatal("Subscription does not publish J2's own local ID")
	}
	listed := callLabGRPC(t, server.Client(), server.URL+listFriendUsersPath, protoBytes(nil, 1, []byte("tenants/current/users/current")), guestToken)
	if listed.status != "0" {
		t.Fatal("ListFriendUsers rejected")
	}
	friendName := decodeTestMessage(t, decodeTestMessage(t, listed.body)[1])[2]
	q := protoBytes(nil, 1, []byte("tenants/current"))
	q = protoBytes(q, 3, []byte("tenants/current/gameSessionSearchConfigs/LCLA6"))
	q = protoBytes(q, 7, friendName)
	answer := callLabGRPC(t, server.Client(), server.URL+queryGameSessionsPath, q, guestToken)
	if answer.status != "0" || !bytes.Contains(answer.body, []byte(host.sessionName)) {
		t.Fatal("ID received from Friends does not find the host's room")
	}
	sentinel := protoBytes(protoBytes(nil, 1, []byte("tenants/current")), 7, []byte("tenants/current/users/INVALID-USER-ID"))
	if got := callLabGRPC(t, server.Client(), server.URL+queryGameSessionsPath, sentinel, guestToken); len(got.body) != 0 {
		t.Fatal("INVALID-USER-ID was treated as a wildcard")
	}
}

func TestLocalFriendPairRejectsPublicOrDuplicatePeers(t *testing.T) {
	for _, cfg := range []string{"127.0.0.1", "127.0.0.1,::1", "127.0.0.1,8.8.8.8", "127.0.0.1,10.77.30.3,10.77.30.4"} {
		if _, err := newLocalFriendPair(cfg); err == nil {
			t.Fatalf("Accepts invalid pair %s", cfg)
		}
	}
}
