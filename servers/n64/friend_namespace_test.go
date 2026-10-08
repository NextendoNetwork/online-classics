package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc/metadata"
	authpb "npln.nintendo.net/npln-practice/proto/auth/v1"
)

func TestConsoleFriendNamespaceSurvivesRefresh(t *testing.T) {
	t.Setenv("NPLN_DEPLOYMENT", "development")
	for _, console := range []bool{false, true} {
		tok := newTokenPID(1800001100, nplnTenant+"/users/u-test", console)
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+tok.AccessToken))
		if consoleFriendsFromContext(ctx) != console {
			t.Fatal("wrong signed namespace")
		}
		// Refresh with no access token exercises persistence across expiry/restart.
		r, err := (&authServer{}).RefreshToken(context.Background(), &authpb.RefreshTokenRequest{User: tok.User, RefreshToken: tok.RefreshToken})
		if err != nil {
			t.Fatal(err)
		}
		ctx = metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+r.Token.AccessToken))
		if consoleFriendsFromContext(ctx) != console {
			t.Fatal("namespace lost on refresh")
		}
		// A friend is named by whatever THEIR platform announces: hardware by its account id,
		// an emulator by its pid, regardless of which kind of client is asking.
		onConsole := nplnFriendData{PID: 1800001100, UserID: "u-friend", AccountHex: "abcdef0123456789",
			Presence: map[string]any{"platform": "switch"}}
		if friendUser("u-test", onConsole, consoleFriendsFromContext(ctx)).NsaId != onConsole.AccountHex {
			t.Fatal("console friend must be named by its account id")
		}
		onEmulator := nplnFriendData{PID: 1800001100, UserID: "u-friend", AccountHex: "abcdef0123456789"}
		if friendUser("u-test", onEmulator, consoleFriendsFromContext(ctx)).NsaId != fmt.Sprintf("%016x", onEmulator.PID) {
			t.Fatal("emulator friend must be named by its pid")
		}
		if _, ok := pidDuJetonRafraichissement(strings.Replace(tok.RefreshToken, "1800001100", "1800001101", 1)); ok {
			t.Fatal("modified refresh accepted")
		}
	}
}

func TestExternalNamespaceUsesAuthenticatedAccount(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"pid":1800001100,"account_hex":"abcdef0123456789"}`)
	}))
	defer s.Close()
	old := accountBaseURL
	accountBaseURL = s.URL
	defer func() { accountBaseURL = old }()
	for _, tc := range []struct {
		sub     string
		console bool
	}{{"abcdef0123456789", true}, {"random-emulator-subject", false}, {"000000006b49d94e", false}} {
		jwt := "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"sub":%q}`, tc.sub))) + ".signature"
		ext := &authpb.ExternalIdToken{Token: &authpb.ExternalIdToken_NsaIdToken{NsaIdToken: jwt}}
		if externalConsoleFriends(ext, 1800001100) != tc.console {
			t.Fatalf("sub %s", tc.sub)
		}
	}
}
