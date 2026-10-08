// SPDX-License-Identifier: MIT
package accountauth

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Friend struct {
	PID        uint64 `json:"pid"`
	UserID     string `json:"user_id"`
	AccountHex string `json:"account_hex"`
}
type Identity struct {
	PID        uint64   `json:"pid"`
	UserID     string   `json:"user_id"`
	AccountHex string   `json:"account_hex"`
	Verified   bool     `json:"verified"`
	Friends    []Friend `json:"friends"`
}

func validIdentity(uid, account string) bool {
	if !strings.HasPrefix(uid, "u-") || len(uid) > 64 || len(uid) < 3 {
		return false
	}
	for _, c := range uid[2:] {
		if !((c >= 'a' && c <= 'z') || (c >= '2' && c <= '7')) {
			return false
		}
	}
	b, e := hex.DecodeString(account)
	return e == nil && len(b) == 8 && account != "0000000000000000"
}

// IdentityClient uses the same configured authority and internal key as the
// online gate. A token or forwarding header cannot choose this destination.
func IdentityClient(c NextendoConfig) (func(uint64) (Identity, error), error) {
	u, err := url.Parse(c.OnlineCheckURL)
	if err != nil || u == nil || u.Path != "/internal/online-check" {
		return nil, errors.New("invalid authority")
	}
	secret := os.Getenv(c.InternalKeyEnv)
	if len(secret) < 32 {
		return nil, errors.New("missing internal key")
	}
	u.Path = "/internal/npln-friends"
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(pid uint64) (Identity, error) {
		var identity Identity
		endpoint := *u
		endpoint.RawQuery = "pid=" + strconv.FormatUint(pid, 10)
		req, e := http.NewRequest("GET", endpoint.String(), nil)
		if e != nil {
			return identity, e
		}
		req.Header.Set("X-Internal-Key", secret)
		res, e := client.Do(req)
		if e != nil {
			return identity, errors.New("account authority unavailable")
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(res.Body, 65537))
		if e != nil || res.StatusCode != 200 || len(raw) > 65536 || json.Unmarshal(raw, &identity) != nil || identity.PID != pid || !identity.Verified || !validIdentity(identity.UserID, identity.AccountHex) || len(identity.Friends) > 512 {
			return Identity{}, errors.New("account identity rejected")
		}
		seen := map[string]bool{}
		for _, f := range identity.Friends {
			if f.PID == 0 || f.PID == pid || !validIdentity(f.UserID, f.AccountHex) || seen[f.UserID] {
				return Identity{}, errors.New("invalid friend graph")
			}
			seen[f.UserID] = true
		}
		return identity, nil
	}, nil
}

// DeviceKind is read only after Authenticate has validated the signed token.
func (a *NextendoAuth) DeviceKind(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return ""
	}
	var h struct{ Kid string }
	if json.Unmarshal(raw, &h) != nil {
		return ""
	}
	return a.deviceKinds[h.Kid]
}

// CheckOnline rechecks an identity established by Authenticate, never a client
// supplied PID. Callers must retain that identity in their server session.
func (a *NextendoAuth) CheckOnline(pid uint64, kind, ip string) bool {
	return a.onlineCheck != nil && a.onlineCheck(pid, kind, ip)
}
