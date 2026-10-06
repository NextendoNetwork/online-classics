package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNPLNSessionProfileVerifiesWithPublishedPublicKey(t *testing.T) {
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	a.sessionProfile = "npln-gss"
	ticket := pendingTicket{owner: "u-test", sessionName: "tenants/" + labTenant + "/gameSessions/session", userName: "tenants/" + labTenant + "/gameSessions/session/userSessions/participant"}
	now := time.Unix(1800000000, 0)
	token, err := a.sessionToken(ticket, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("Token is not JWT")
	}
	decode := func(s string, dest any) {
		t.Helper()
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, dest); err != nil {
			t.Fatal(err)
		}
	}
	var header map[string]string
	decode(parts[0], &header)
	if header["jku"] != "https://"+labTenant+".lp1.t.npln.srv.nintendo.net/jwkSets/nplnAccessToken" {
		t.Fatal("JWK does not point to the lab")
	}
	var claims struct {
		Issuer  string `json:"iss"`
		Subject string `json:"sub"`
		Issued  int64  `json:"iat"`
		Expires int64  `json:"exp"`
		NPLN    struct {
			Tenant  string `json:"tid"`
			App     string `json:"app_id"`
			Account string `json:"aid"`
		} `json:"npln"`
		GSS struct {
			Session string `json:"game_session"`
			User    string `json:"user_session"`
		} `json:"gss"`
	}
	decode(parts[1], &claims)
	if claims.Issuer != "default iss" || claims.Subject != ticket.owner || claims.Issued != now.Unix() || claims.Expires-claims.Issued != 8*3600 || claims.NPLN.Tenant != labTenant || claims.NPLN.App != "0100c62011050000" || claims.NPLN.Account != "a-test" || claims.GSS.Session != ticket.sessionName || claims.GSS.User != ticket.userName {
		t.Fatal("Incorrect profile or references")
	}
	rr := httptest.NewRecorder()
	a.publicKeySet(rr, httptest.NewRequest("GET", "/jwkSets/nplnAccessToken", nil))
	var jwks struct {
		Keys []map[string]string `json:"keys"`
	}
	if json.Unmarshal(rr.Body.Bytes(), &jwks) != nil || len(jwks.Keys) != 1 {
		t.Fatal("Invalid JWK")
	}
	k := jwks.Keys[0]
	if _, private := k["d"]; private {
		t.Fatal("JWK exposes private key")
	}
	if k["kid"] != header["kid"] || k["crv"] != "P-256" {
		t.Fatal("Key does not match JWT")
	}
	x, err := base64.RawURLEncoding.DecodeString(k["x"])
	if err != nil {
		t.Fatal(err)
	}
	y, err := base64.RawURLEncoding.DecodeString(k["y"])
	if err != nil {
		t.Fatal(err)
	}
	pub := ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatal("Invalid signature")
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&pub, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("Signature does not verify against published JWK")
	}
}
