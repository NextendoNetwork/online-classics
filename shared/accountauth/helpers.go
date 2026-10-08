// SPDX-License-Identifier: MIT
package accountauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

type Object = map[string]any

func str(v any) string { s, _ := v.(string); return s }
func number(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	value, err := n.Int64()
	return value, err == nil
}

// Authenticate validates the external signed token and the enclosed account
// proof through the configured account authority and online-check. It never
// derives the account from an unsigned JWT or a caller-supplied network address.
func (a *NextendoAuth) Authenticate(token, peerIP string) (uint64, error) {
	if len(token) == 0 || len(token) > 4096 {
		return 0, errors.New("credential rejected")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, errors.New("credential rejected")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, errors.New("credential rejected")
	}
	var claims struct {
		Subject string `json:"sub"`
	}
	if json.Unmarshal(raw, &claims) != nil || claims.Subject == "" {
		return 0, errors.New("credential rejected")
	}
	identity, ok := a.VerifyForPeer(token, claims.Subject, peerIP)
	pid, err := strconv.ParseUint(identity, 10, 64)
	if !ok || err != nil || pid == 0 {
		return 0, errors.New("account verification or online gate rejected")
	}
	return pid, nil
}
