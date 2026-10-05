package main

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestSessionTokenPreservesUserDataAndValidSignature(t *testing.T) {
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	entry := protoBytes(nil, 1, []byte("counter"))
	entry = protoBytes(entry, 2, protoVarint(nil, 3, 9007199254740993))
	attrs := protoBytes(nil, 1, entry)
	latency := protoBytes(nil, 1, []byte("local"))
	latency = protoBytes(latency, 2, protoVarint(nil, 2, 123000000))
	user := protoBytes(nil, 1, []byte("tenants/"+labTenant+"/users/u-test"))
	user = protoBytes(user, 2, attrs)
	user = protoBytes(user, 3, protoBytes(nil, 1, latency))
	user = protoBytes(user, 4, []byte("blue"))
	ticket := pendingTicket{owner: "u-test", user: user, sessionName: "tenants/" + labTenant + "/gameSessions/session123", userName: "tenants/" + labTenant + "/gameSessions/session123/userSessions/participant123"}
	now := time.Unix(1800000000, 0)
	token, err := a.sessionToken(ticket, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("Invalid JWT")
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]string
	if json.Unmarshal(header, &meta) != nil || len(meta) != 2 || meta["alg"] != "ES256" || meta["kid"] != "genesis-lab" {
		t.Fatal("Incorrect gss header")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Issuer   string `json:"iss"`
		Subject  string `json:"sub"`
		Issued   int64  `json:"iat"`
		Expires  int64  `json:"exp"`
		GameSync struct {
			Attr        string `json:"attr"`
			Latency     string `json:"ltcy"`
			Team        string `json:"team"`
			Session     string `json:"gsid"`
			UserSession string `json:"usid"`
			User        string `json:"uid"`
			Tenant      string `json:"tid"`
		} `json:"gamesync"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		t.Fatal("Invalid claims")
	}
	if claims.Issuer != "gss" || claims.Subject != ticket.owner || claims.Issued != now.Unix() || claims.Expires-claims.Issued != 3600 {
		t.Fatal("Incorrect identity or validity period")
	}
	g := claims.GameSync
	if g.Attr != `{"counter":{"type":"integer","value":9007199254740993}}` || g.Latency != `{"latencies":{"local":{"nanos":123000000}}}` || g.Team != "blue" || g.User != ticket.owner || g.Tenant != labTenant || g.Session != "session123" || g.UserSession != "participant123" {
		t.Fatal("User data or session references were lost")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatal("Invalid signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&a.key.PublicKey, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("Unverifiable signature")
	}
}

func TestSessionTokenRejectsMalformedAttributes(t *testing.T) {
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	_, err = a.sessionToken(pendingTicket{user: protoBytes(nil, 2, []byte{0xff})}, time.Now())
	if err == nil {
		t.Fatal("Must not replace invalid attributes with an empty map")
	}
}
