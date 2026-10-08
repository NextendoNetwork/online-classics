// SPDX-License-Identifier: MIT
package accountauth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSignedAccountBoundaryAndGate(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encode := base64.RawURLEncoding.EncodeToString
	proof := "nx2." + encode([]byte("123.Tester.2000")) + ".authority-fixture"
	allow := true
	calls := 0
	authority := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/api/profile":
			if r.Header.Get("Authorization") != "Bearer "+proof {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"username":"Tester"}`))
		case "/internal/online-check":
			if r.Header.Get("X-Internal-Key") != strings.Repeat("k", 32) {
				w.WriteHeader(401)
				return
			}
			var body struct {
				PID      uint64
				Kind, IP string
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.PID != 123 || body.Kind != "ryujinx" || body.IP != "127.0.0.1" {
				t.Error("gate request identity mismatch")
				w.WriteHeader(400)
				return
			}
			json.NewEncoder(w).Encode(Object{"allow": allow, "session_id": "isolated-session"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer authority.Close()
	path := filepath.Join(t.TempDir(), "jwks.json")
	raw, _ := json.Marshal(Object{"keys": []Object{{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "emulator", "n": encode(key.N.Bytes()), "e": encode([]byte{1, 0, 1})}}})
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("fixture JWKS")
	}
	t.Setenv("CLASSICS_TEST_INTERNAL_KEY", strings.Repeat("k", 32))
	cfg := NextendoConfig{TitleID: "010012F017576000", JWKSPath: path, Issuer: "fixture", Audience: "classics-fixture", AllowAllAccounts: true, ProfileURL: authority.URL + "/api/profile", OnlineCheckURL: authority.URL + "/internal/online-check", InternalKeyEnv: "CLASSICS_TEST_INTERNAL_KEY", DeviceKindsByKeyID: map[string]string{"emulator": "ryujinx"}}
	auth, err := NewNextendoAuth(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	auth.now = func() time.Time { return time.Unix(1000, 0) }
	mint := func(changes Object) string {
		header, _ := json.Marshal(Object{"alg": "RS256", "kid": "emulator"})
		claims := Object{"iss": "fixture", "aud": "classics-fixture", "sub": "client", "iat": 900, "exp": 1500, "app_id": cfg.TitleID, "nnex": proof}
		for k, v := range changes {
			claims[k] = v
		}
		body, _ := json.Marshal(claims)
		input := encode(header) + "." + encode(body)
		sum := sha256.Sum256([]byte(input))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		return input + "." + encode(sig)
	}
	valid := mint(nil)
	if pid, err := auth.Authenticate(valid, "127.0.0.1"); err != nil || pid != 123 {
		t.Fatal("verified account rejected", err)
	}
	before := calls
	for _, change := range []Object{{"iss": "other"}, {"aud": "other"}, {"app_id": "0100000000000000"}, {"app_id": nil}, {"exp": 999}, {"iat": 2000}, {"nnex": nil}} {
		if _, err := auth.Authenticate(mint(change), "127.0.0.1"); err == nil {
			t.Fatal("invalid credential accepted")
		}
	}
	parts := strings.Split(valid, ".")
	signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
	signature[0] ^= 1
	parts[2] = encode(signature)
	if _, err := auth.Authenticate(strings.Join(parts, "."), "127.0.0.1"); err == nil {
		t.Fatal("forged signature accepted")
	}
	if calls != before {
		t.Fatal("invalid signature or scope contacted account authority")
	}
	allow = false
	if _, err := auth.Authenticate(valid, "127.0.0.1"); err == nil {
		t.Fatal("denied online gate accepted")
	}
	bad := cfg
	bad.OnlineCheckURL = ""
	if _, err := NewNextendoAuth(bad, nil); err == nil {
		t.Fatal("missing account gate accepted")
	}
	bad = cfg
	bad.ProfileURL = "http://accounts.example/api/profile"
	if _, err := NewNextendoAuth(bad, nil); err == nil {
		t.Fatal("public plaintext authority accepted")
	}
}
