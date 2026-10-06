package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"sort"
	"strings"

	"google.golang.org/grpc/metadata"
	authpb "npln.nintendo.net/npln-practice/proto/auth/v1"
)

// Call only after gatedIdentity succeeds. This selects a presentation namespace,
// never an authentication identity: native friends use BAAS IDs, emulator friends
// use PIDs. Compare the subject with the authenticated account, not arbitrary IDs.
func externalConsoleFriends(ext *authpb.ExternalIdToken, pid uint64) bool {
	if ext == nil {
		return false
	}
	if c, ok := parseSubsdkToken(ext.GetNsaIdToken()); ok {
		return !c.Emulator
	}
	sub, ok := nsaFromExternal(ext)
	if !ok {
		return false
	}
	account, err := accountFriends(pid)
	console := err == nil && account.AccountHex != "" && strings.EqualFold(sub, account.AccountHex)
	// Console and emulator both present a BAAS token, so log what separates them: an emulator
	// resolved as console is handed account-hex ids its pid-keyed friend cache cannot match.
	log.Printf("[NPLN Friends] id form pid=%d console=%t sub=%q claims=%v", pid, console, sub, idTokenClaimKeys(ext.GetNsaIdToken()))
	return console
}

// idTokenClaimKeys lists the claim names of a BAAS id token, never their values.
func idTokenClaimKeys(jwt string) []string {
	parts := strings.Split(jwt, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	keys := make([]string, 0, len(claims))
	for k := range claims {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func consoleFriendsFromContext(ctx context.Context) bool {
	md, _ := metadata.FromIncomingContext(ctx)
	for _, value := range md.Get("authorization") {
		parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(value), "Bearer "), "bearer "), ".")
		if len(parts) != 3 || !verifyNplnAccessToken(parts[0], parts[1], parts[2]) {
			continue
		}
		raw, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			continue
		}
		var c struct {
			Console bool `json:"nextendo_console_friends"`
		}
		if json.Unmarshal(raw, &c) == nil {
			return c.Console
		}
	}
	return false
}
