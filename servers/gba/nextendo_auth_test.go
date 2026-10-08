package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NextendoNetwork/online-classics/accountauth"
	"github.com/pion/logging"
	"github.com/pion/turn/v5"
)

func accountAuthFixture(t *testing.T) (*labAuth, func(uint64) string, *atomic.Bool) {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	encode := base64.RawURLEncoding.EncodeToString
	enabled := new(atomic.Bool)
	enabled.Store(true)
	internal := strings.Repeat("k", 32)
	t.Setenv("CLASSICS_FIXTURE_KEY", internal)
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/profile":
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer nx2.") {
				w.WriteHeader(401)
				return
			}
			io.WriteString(w, `{"username":"Fixture"}`)
		case "/internal/online-check":
			if r.Header.Get("X-Internal-Key") != internal {
				w.WriteHeader(401)
				return
			}
			var request struct {
				PID      uint64
				Kind, IP string
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.PID < 123 || request.PID > 124 || request.Kind != "ryujinx" || net.ParseIP(request.IP) == nil {
				w.WriteHeader(400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"allow": enabled.Load(), "session_id": "test-session"})
		case "/internal/npln-friends":
			if r.Header.Get("X-Internal-Key") != internal {
				w.WriteHeader(401)
				return
			}
			pid, _ := strconv.ParseUint(r.URL.Query().Get("pid"), 10, 64)
			uid, nsa, other, otherUID, otherNSA := "u-aaaa", "0102030405060708", uint64(124), "u-bbbb", "1112131415161718"
			if pid == 124 {
				uid, nsa, other, otherUID, otherNSA = otherUID, otherNSA, 123, uid, nsa
			}
			json.NewEncoder(w).Encode(accountauth.Identity{PID: pid, UserID: uid, AccountHex: nsa, Verified: enabled.Load(), Friends: []accountauth.Friend{{PID: other, UserID: otherUID, AccountHex: otherNSA}}})
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(authority.Close)
	jwks := filepath.Join(t.TempDir(), "jwks.json")
	raw, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "emulator", "n": encode(key.N.Bytes()), "e": "AQAB"}}})
	if os.WriteFile(jwks, raw, 0600) != nil {
		t.Fatal("fixture JWKS")
	}
	cfg := accountauth.NextendoConfig{TitleID: nextendoTitleID, JWKSPath: jwks, Issuer: "isolated-fixture", Audience: "classics", AllowAllAccounts: true, ProfileURL: authority.URL + "/api/profile", OnlineCheckURL: authority.URL + "/internal/online-check", InternalKeyEnv: "CLASSICS_FIXTURE_KEY", DeviceKindsByKeyID: map[string]string{"emulator": "ryujinx"}}
	verifier, e := accountauth.NewNextendoAuth(cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	verifier.ObserveVerification = func(stage string) { t.Log(stage) }
	identity, e := accountauth.IdentityClient(cfg)
	if e != nil {
		t.Fatal(e)
	}
	a, e := newLabAuth()
	if e != nil {
		t.Fatal(e)
	}
	a.nextendo = &nextendoSessions{verifier: verifier, identity: identity, sessions: map[string]accountSession{}, refresh: map[[32]byte]string{}}
	mint := func(pid uint64) string {
		now := time.Now().Unix()
		proof := "nx2." + encode([]byte(strconv.FormatUint(pid, 10)+".Fixture."+strconv.FormatInt(now+600, 10))) + ".authority-proof"
		header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "emulator"})
		claims, _ := json.Marshal(map[string]any{"iss": cfg.Issuer, "aud": cfg.Audience, "sub": "client", "app_id": nextendoTitleID, "iat": now, "exp": now + 600, "nnex": proof})
		input := encode(header) + "." + encode(claims)
		hash := sha256.Sum256([]byte(input))
		sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
		if e != nil {
			t.Fatal(e)
		}
		return input + "." + encode(sig)
	}
	return a, mint, enabled
}

func accountRequest(path string, payload []byte, token string) *http.Request {
	r := httptest.NewRequest("POST", path, bytes.NewReader(streamTestFrame(payload)))
	r.RemoteAddr = "127.0.0.1:55000"
	r.Header.Set("Content-Type", "application/grpc")
	r.Header.Set("npln-tenant-id", labTenant)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestNextendoAccountsShareIPWithoutSharingIdentity(t *testing.T) {
	a, mint, enabled := accountAuthFixture(t)
	uid, access := authenticatePairTest(t, a, "127.0.0.1", mint(123))
	other, _ := authenticatePairTest(t, a, "127.0.0.1", mint(124))
	if uid != "u-aaaa" || other != "u-bbbb" {
		t.Fatal("accounts merged behind NAT")
	}
	r := accountRequest(listFriendUsersPath, protoBytes(nil, 1, []byte("tenants/current/users/current")), access)
	w := httptest.NewRecorder()
	a.listLocalFriends(w, r)
	if w.Result().Trailer.Get("Grpc-Status") != "0" || !bytes.Contains(w.Body.Bytes(), []byte(other)) || !bytes.Contains(w.Body.Bytes(), []byte("1112131415161718")) {
		t.Fatal("authority friend identity lost")
	}
	enabled.Store(false)
	w = httptest.NewRecorder()
	a.listLocalFriends(w, accountRequest(listFriendUsersPath, protoBytes(nil, 1, []byte("tenants/current/users/current")), access))
	if w.Result().Trailer.Get("Grpc-Status") != "16" {
		t.Fatal("revoked account still authorized")
	}
}

func TestNextendoRejectsLabCredentialAndRotatesRefresh(t *testing.T) {
	a, mint, _ := accountAuthFixture(t)
	payload := func(token string) []byte {
		return protoBytes(protoBytes(nil, 1, []byte("tenants/"+labTenant)), 3, protoBytes(nil, 2, []byte(token)))
	}
	w := httptest.NewRecorder()
	a.issuePrearrangedUserToken(w, accountRequest(issuePrearrangedUserTokenPath, payload("unsigned-lab-token"), ""))
	if w.Result().Trailer.Get("Grpc-Status") != "16" {
		t.Fatal("lab credential accepted")
	}
	w = httptest.NewRecorder()
	a.issuePrearrangedUserToken(w, accountRequest(issuePrearrangedUserTokenPath, payload(mint(123)), ""))
	if w.Result().Trailer.Get("Grpc-Status") != "0" {
		t.Fatal("verified login rejected")
	}
	token := decodeTestMessage(t, decodeTestMessage(t, w.Body.Bytes()[5:])[2])
	refresh, oldAccess := string(token[3]), string(token[2])
	req := protoBytes(protoBytes(nil, 1, []byte("tenants/current/users/current")), 2, []byte(refresh))
	w = httptest.NewRecorder()
	a.refreshAccountToken(w, accountRequest(refreshTokenPath, req, ""))
	if w.Result().Trailer.Get("Grpc-Status") != "0" {
		t.Fatal("refresh rejected")
	}
	if _, ok := a.verifyJWT(oldAccess); !ok {
		t.Fatal("refresh interrupted active account session")
	}
	w = httptest.NewRecorder()
	a.refreshAccountToken(w, accountRequest(refreshTokenPath, req, ""))
	if w.Result().Trailer.Get("Grpc-Status") != "16" {
		t.Fatal("refresh replay accepted")
	}
	h := handlerWithConfiguredAuth(a, log.New(io.Discard, "", 0), "gss", iceEndpoint{}, nil, nil, false, false, false)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/lab/rooms", nil))
	if w.Code != 404 {
		t.Fatal("unauthenticated lab room API exposed")
	}
}

func TestGamesyncCannotOutliveItsVerifiedAccount(t *testing.T) {
	a, mint, enabled := accountAuthFixture(t)
	uid, access := authenticatePairTest(t, a, "127.0.0.1", mint(123))
	claims, ok := a.verifyJWT(access)
	if !ok {
		t.Fatal("account access token rejected")
	}
	store := newTicketStore()
	raw := "private-gamesync-fixture"
	store.issuedGamesync[sha256.Sum256([]byte(raw))] = issuedMatch{ticket: pendingTicket{owner: uid, accountSession: claims.Session}, expires: time.Now().Add(time.Hour)}
	called := false
	protected := a.enforceGamesyncAccounts(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }), store)
	w := httptest.NewRecorder()
	protected.ServeHTTP(w, accountRequest(gamesyncGetDocumentPath, nil, raw))
	if !called || w.Code != 204 {
		t.Fatal("verified Gamesync rejected")
	}
	enabled.Store(false)
	called = false
	w = httptest.NewRecorder()
	protected.ServeHTTP(w, accountRequest(gamesyncGetDocumentPath, nil, raw))
	if called || w.Result().Trailer.Get("Grpc-Status") != "16" {
		t.Fatal("Gamesync bypassed revoked account gate")
	}
}

func TestNextendoTURNRequiresIssuedAccountCredentialAndUsesBoundedPorts(t *testing.T) {
	cfg := nextendoDeployment{AdvertisedIPv4: "127.0.0.1", UDPBindIPv4: "127.0.0.1", TURNPort: 0, RelayMinPort: 48000, RelayMaxPort: 48031, IsolatedLoopbackTest: true}
	relay, e := startNextendoTURN(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer relay.Close()
	allowed := new(atomic.Bool)
	allowed.Store(true)
	user, pass, e := relay.credentialsForAccount(time.Now(), func(ip string) bool { return ip == "127.0.0.1" && allowed.Load() })
	if e != nil {
		t.Fatal(e)
	}
	// Even knowing the process HMAC secret cannot bypass the issuance registry.
	unissued, unissuedPassword, e := relay.credentials(time.Now())
	if e != nil {
		t.Fatal(e)
	}
	allocate := func(username, password string) (net.PacketConn, error) {
		conn, e := net.ListenPacket("udp4", "127.0.0.1:0")
		if e != nil {
			return nil, e
		}
		t.Cleanup(func() { conn.Close() })
		factory := logging.NewDefaultLoggerFactory()
		factory.Writer = io.Discard
		client, e := turn.NewClient(&turn.ClientConfig{Conn: conn, TURNServerAddr: net.JoinHostPort(relay.endpoint.host, strconv.Itoa(relay.endpoint.port)), Username: username, Password: password, Realm: "nextendo-classics", RTO: 10 * time.Millisecond, LoggerFactory: factory})
		if e != nil {
			return nil, e
		}
		t.Cleanup(client.Close)
		if e = client.Listen(); e != nil {
			return nil, e
		}
		return client.Allocate()
	}
	allocated, e := allocate(user, pass)
	if e != nil {
		t.Fatal(e)
	}
	defer allocated.Close()
	port := allocated.LocalAddr().(*net.UDPAddr).Port
	if port < 48000 || port > 48031 {
		t.Fatal("unbounded relay port")
	}
	if p, e := allocate(unissued, unissuedPassword); e == nil {
		p.Close()
		t.Fatal("unissued TURN credential accepted")
	}
	allowed.Store(false)
	if p, e := allocate(user, pass); e == nil {
		p.Close()
		t.Fatal("revoked account could allocate")
	}
	for _, ip := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "169.254.169.254", "100.64.0.1", "198.18.0.1", "224.0.0.1", "::1"} {
		if publicPeerIP(net.ParseIP(ip)) {
			t.Fatalf("private peer allowed: %s", ip)
		}
	}
}
