package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/NextendoNetwork/online-classics/accountauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"net"
	"net/http"
	"net/http/httptest"
	authpb "npln.nintendo.net/npln-practice/proto/auth/v1"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNextendoNeverUsesLegacyIdentityFallback(t *testing.T) {
	t.Setenv("NPLN_DEPLOYMENT", "nextendo")
	ext := &authpb.ExternalIdToken{Token: &authpb.ExternalIdToken_DummyExtIdToken{DummyExtIdToken: "dummy:1800000001"}}
	if _, _, err := gatedIdentity(ext, nplnTenant); status.Code(err) != codes.Unauthenticated {
		t.Fatal("development fallback exposed")
	}
	if _, err := (&authServer{}).IssuePrearrangedUserToken(context.Background(), &authpb.IssuePrearrangedUserTokenRequest{Tenant: nplnTenant, ExternalIdToken: ext}); status.Code(err) != codes.Unauthenticated {
		t.Fatal("unprovisioned production auth accepted")
	}
	t.Setenv("NPLN_ACCOUNT_CONFIG", "")
	if configureAccountVerifier() == nil {
		t.Fatal("missing verifier configuration accepted")
	}
}

func TestNextendoSignedLoginAndRevokedRefresh(t *testing.T) {
	t.Setenv("NPLN_DEPLOYMENT", "nextendo")
	t.Setenv("N64_FIXTURE_KEY", strings.Repeat("k", 32))
	enabled := new(atomic.Bool)
	enabled.Store(true)
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/profile":
			json.NewEncoder(w).Encode(map[string]string{"username": "Fixture"})
		case "/internal/online-check":
			if r.Header.Get("X-Internal-Key") != strings.Repeat("k", 32) {
				w.WriteHeader(401)
				return
			}
			var request struct {
				PID      uint64
				Kind, IP string
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.PID != 123 || request.Kind != "ryujinx" || request.IP != "127.0.0.1" {
				w.WriteHeader(400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"allow": enabled.Load(), "session_id": "fixture"})
		case "/internal/npln-friends":
			if r.Header.Get("X-Internal-Key") != strings.Repeat("k", 32) || r.URL.Query().Get("pid") != "123" {
				w.WriteHeader(401)
				return
			}
			json.NewEncoder(w).Encode(accountauth.Identity{PID: 123, UserID: "u-aaaa", AccountHex: "0102030405060708", Verified: enabled.Load()})
		default:
			w.WriteHeader(404)
		}
	}))
	defer authority.Close()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	jwks := filepath.Join(t.TempDir(), "jwks.json")
	raw, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "emulator", "n": encode(key.N.Bytes()), "e": "AQAB"}}})
	if os.WriteFile(jwks, raw, 0600) != nil {
		t.Fatal("fixture key")
	}
	cfg := accountauth.NextendoConfig{TitleID: nplnAppID, JWKSPath: jwks, Issuer: "fixture", Audience: "n64", AllowAllAccounts: true, ProfileURL: authority.URL + "/api/profile", OnlineCheckURL: authority.URL + "/internal/online-check", InternalKeyEnv: "N64_FIXTURE_KEY", DeviceKindsByKeyID: map[string]string{"emulator": "ryujinx"}}
	path := filepath.Join(t.TempDir(), "account.json")
	raw, _ = json.Marshal(cfg)
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("fixture config")
	}
	t.Setenv("NPLN_ACCOUNT_CONFIG", path)
	oldVerifier, oldIdentity := deploymentAccountVerifier, deploymentAccountIdentity
	t.Cleanup(func() { deploymentAccountVerifier, deploymentAccountIdentity = oldVerifier, oldIdentity })
	if err = configureAccountVerifier(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	// The authority fixture owns the proof's expiry; only the signed BAAS wrapper
	// and the authority response establish identity in this test.
	proof := "nx2." + encode([]byte(fmt.Sprintf("123.Fixture.%d", now+600))) + ".fixture-proof"
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "emulator"})
	claims, _ := json.Marshal(map[string]any{"iss": cfg.Issuer, "aud": cfg.Audience, "sub": "client", "app_id": nplnAppID, "iat": now, "exp": now + 600, "nnex": proof})
	input := encode(header) + "." + encode(claims)
	hash := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	ext := &authpb.ExternalIdToken{Token: &authpb.ExternalIdToken_NsaIdToken{NsaIdToken: input + "." + encode(sig)}}
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}})
	srv := &authServer{}
	response, err := srv.IssuePrearrangedUserToken(ctx, &authpb.IssuePrearrangedUserTokenRequest{Tenant: nplnTenant, ExternalIdToken: ext})
	if err != nil || response.GetUser().GetName() != nplnTenant+"/users/u-aaaa" {
		t.Fatal("canonical signed account login failed", err)
	}
	enabled.Store(false)
	if _, err = srv.RefreshToken(ctx, &authpb.RefreshTokenRequest{User: response.Token.User, RefreshToken: response.Token.RefreshToken}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("revoked account refreshed", err)
	}
}
