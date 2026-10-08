// SPDX-License-Identifier: MIT
package accountauth

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// NextendoConfig enrolls an operator-trusted public JWKS and exact BAAS scope.
// Token jku/x5u values never select a network destination or signing key.
type NextendoConfig struct {
	TitleID            string            `json:"titleId"`
	JWKSPath           string            `json:"jwksPath"`
	Issuer             string            `json:"issuer"`
	Audience           string            `json:"audience"`
	AllowAllAccounts   bool              `json:"allowAllAccounts"`
	OnlineCheckURL     string            `json:"onlineCheckURL"`
	InternalKeyEnv     string            `json:"internalKeyEnv"`
	DeviceKindsByKeyID map[string]string `json:"deviceKindsByKeyId"`
	ProfileURL         string            `json:"profileURL"`
}

type NextendoAuth struct {
	// ObserveVerification receives fixed stage labels only, never credential data.
	ObserveVerification func(string)
	keys                map[string]*rsa.PublicKey
	issuer, audience    string
	title               string
	allowed             map[string]bool
	now                 func() time.Time
	profile             func(string) (string, error)
	onlineCheck         func(uint64, string, string) bool
	deviceKinds         map[string]string
}

func NewNextendoAuth(c NextendoConfig, subjects []string) (*NextendoAuth, error) {
	if c.TitleID == "" || c.OnlineCheckURL == "" || c.Issuer == "" || c.Audience == "" || (!c.AllowAllAccounts && len(subjects) == 0) || (c.AllowAllAccounts && len(subjects) != 0) {
		return nil, errors.New("explicit Nextendo scope and enrolled accounts required")
	}
	raw, err := os.ReadFile(c.JWKSPath)
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("trusted public JWKS unavailable")
	}
	var set struct {
		Keys []struct{ Kty, Use, Alg, Kid, N, E string }
	}
	if json.Unmarshal(raw, &set) != nil || len(set.Keys) == 0 || len(set.Keys) > 16 {
		return nil, errors.New("invalid public JWKS")
	}
	a := &NextendoAuth{keys: map[string]*rsa.PublicKey{}, issuer: c.Issuer, audience: c.Audience, title: c.TitleID, allowed: map[string]bool{}, now: time.Now, profile: nextendoProfile}
	if c.ProfileURL != "" {
		u, err := url.Parse(c.ProfileURL)
		ip := net.ParseIP(uHostname(u))
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/api/profile" ||
			(u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback())) {
			return nil, errors.New("invalid trusted account profile URL")
		}
		a.profile = profileClient(c.ProfileURL)
	}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Use != "sig" || k.Alg != "RS256" {
			continue
		}
		n, en := base64.RawURLEncoding.DecodeString(k.N)
		e, ee := base64.RawURLEncoding.DecodeString(k.E)
		modulus, exponent := new(big.Int).SetBytes(n), new(big.Int).SetBytes(e)
		if en != nil || ee != nil || modulus.BitLen() < 2048 || modulus.BitLen() > 8192 || exponent.Int64() != 65537 || k.Kid == "" || a.keys[k.Kid] != nil {
			return nil, errors.New("invalid or duplicate RSA key")
		}
		a.keys[k.Kid] = &rsa.PublicKey{N: modulus, E: 65537}
	}
	if len(a.keys) == 0 {
		return nil, errors.New("no enrolled RS256 keys")
	}
	for _, s := range subjects {
		id, e := strconv.ParseUint(s, 10, 64)
		if e != nil || id == 0 || strconv.FormatUint(id, 10) != s {
			return nil, errors.New("Nextendo identities must be canonical decimal PIDs")
		}
		a.allowed[s] = true
	}
	if c.AllowAllAccounts {
		a.allowed = nil // Account authority and online gate decide eligibility.
	}
	if c.OnlineCheckURL != "" {
		u, err := url.Parse(c.OnlineCheckURL)
		ip := net.ParseIP(uHostname(u))
		if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/internal/online-check" ||
			(u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback())) {
			return nil, errors.New("online-check requires HTTPS or literal loopback HTTP and the exact internal route")
		}
		secret := os.Getenv(c.InternalKeyEnv)
		if c.InternalKeyEnv == "" || len(secret) < 32 || strings.ContainsAny(secret, "\r\n") || len(c.DeviceKindsByKeyID) == 0 {
			return nil, errors.New("online-check requires a private internal key and trusted signing-key device mapping")
		}
		a.deviceKinds = map[string]string{}
		for kid, kind := range c.DeviceKindsByKeyID {
			if a.keys[kid] == nil || (kind != "switch" && kind != "ryujinx") {
				return nil, errors.New("invalid trusted device-kind mapping")
			}
			a.deviceKinds[kid] = kind
		}
		a.onlineCheck = onlineCheckClient(c.OnlineCheckURL, secret)
	}
	return a, nil
}

func uHostname(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Hostname()
}

// The route and secret come only from private operator configuration.
// Neither a credential nor forwarding headers can choose this destination.
func onlineCheckClient(endpoint, secret string) func(uint64, string, string) bool {
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(pid uint64, kind, ip string) bool {
		if pid == 0 || net.ParseIP(ip) == nil || (kind != "switch" && kind != "ryujinx") {
			return false
		}
		body, _ := json.Marshal(struct {
			PID  uint64 `json:"pid"`
			Kind string `json:"kind"`
			IP   string `json:"ip"`
		}{pid, kind, ip})
		req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
		if err != nil {
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Internal-Key", secret)
		res, err := client.Do(req)
		if err != nil {
			return false
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(res.Body, 65537))
		if err != nil || res.StatusCode != 200 || len(raw) > 65536 {
			return false
		}
		var result struct {
			Allow     *bool  `json:"allow"`
			SessionID string `json:"session_id"`
		}
		return json.Unmarshal(raw, &result) == nil && result.Allow != nil && *result.Allow && result.SessionID != ""
	}
}

func nextendoProfile(proof string) (string, error) {
	return profileClient("https://nextendo.network/api/profile")(proof)
}

func profileClient(endpoint string) func(string) (string, error) {
	return func(proof string) (string, error) {
		client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		request, err := http.NewRequest("GET", endpoint, nil)
		if err != nil {
			return "", errors.New("invalid account profile URL")
		}
		request.Header.Set("Authorization", "Bearer "+proof)
		response, err := client.Do(request)
		if err != nil {
			return "", errors.New("account verification unavailable")
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
		if err != nil || response.StatusCode != 200 || len(raw) > 65536 {
			return "", errors.New("account proof rejected")
		}
		var result struct {
			Username string `json:"username"`
		}
		if json.Unmarshal(raw, &result) != nil || result.Username == "" {
			return "", errors.New("invalid account response")
		}
		return result.Username, nil
	}
}

// Verify returns the account PID only after checking the BAAS signature, scope,
// external identity and the enclosed nx2 proof against the account authority.
func (a *NextendoAuth) Verify(token, external string) (string, bool) {
	return a.VerifyForPeer(token, external, "")
}

func (a *NextendoAuth) VerifyForPeer(token, external, peerIP string) (string, bool) {
	stage := "framing"
	defer func() {
		if a.ObserveVerification != nil {
			a.ObserveVerification(stage)
		}
	}()
	if len(token) == 0 || len(token) > 4096 || len(external) == 0 || len(external) > 128 {
		return "", false
	}
	trimmed := strings.TrimRight(token, "\x00")
	if len(token)-len(trimmed) > 2 || strings.ContainsRune(trimmed, 0) {
		return "", false
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return "", false
	}
	headerRaw, e := base64.RawURLEncoding.DecodeString(parts[0])
	stage = "algorithm-or-key"
	if e != nil {
		return "", false
	}
	var header struct{ Alg, Kid string }
	if json.Unmarshal(headerRaw, &header) != nil || header.Alg != "RS256" {
		return "", false
	}
	key := a.keys[header.Kid]
	if key == nil {
		return "", false
	}
	signature, e := base64.RawURLEncoding.DecodeString(parts[2])
	stage = "signature"
	if e != nil {
		return "", false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return "", false
	}
	claimsRaw, e := base64.RawURLEncoding.DecodeString(parts[1])
	stage = "scope-or-time"
	if e != nil {
		return "", false
	}
	var claims Object
	decoder := json.NewDecoder(bytes.NewReader(claimsRaw))
	decoder.UseNumber()
	if decoder.Decode(&claims) != nil || claims == nil {
		return "", false
	}
	exp, ok := number(claims["exp"])
	iat, issued := number(claims["iat"])
	now := a.now().Unix()
	if !ok || !issued || now >= exp || iat > now+30 || str(claims["iss"]) != a.issuer || str(claims["aud"]) != a.audience || !strings.EqualFold(str(claims["sub"]), external) {
		return "", false
	}
	if v, exists := claims["nbf"]; exists {
		n, valid := number(v)
		if !valid || n > now+30 {
			return "", false
		}
	}
	if !strings.EqualFold(str(claims["app_id"]), a.title) {
		return "", false
	}
	proof := str(claims["nnex"])
	stage = "proof-or-enrollment"
	if len(proof) > 2048 || !strings.HasPrefix(proof, "nx2.") {
		return "", false
	}
	proofParts := strings.Split(proof, ".")
	if len(proofParts) != 3 {
		return "", false
	}
	raw, e := base64.RawURLEncoding.DecodeString(proofParts[1])
	if e != nil {
		return "", false
	}
	identity := strings.SplitN(string(raw), ".", 3)
	if len(identity) != 3 || (a.allowed != nil && !a.allowed[identity[0]]) {
		return "", false
	}
	accountPID, err := strconv.ParseUint(identity[0], 10, 64)
	if err != nil || accountPID == 0 || strconv.FormatUint(accountPID, 10) != identity[0] {
		return "", false
	}
	expiry, e := strconv.ParseInt(identity[2], 10, 64)
	if e != nil || now >= expiry {
		return "", false
	}
	username, e := a.profile(proof)
	stage = "account-authority"
	if e != nil || username != identity[1] {
		return "", false
	}
	if a.onlineCheck != nil {
		stage = "online-gate"
		if !a.onlineCheck(accountPID, a.deviceKinds[header.Kid], peerIP) {
			return "", false
		}
	}
	stage = "verified"
	return identity[0], true
}
