package main

import (
	"bytes"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGamesyncIssueTokenRequiresOwnMatchingUnexpiredTicket(t *testing.T) {
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	ticket := pendingTicket{owner: "u-test", sessionName: "tenants/" + labTenant + "/gameSessions/room", userName: "tenants/" + labTenant + "/gameSessions/room/userSessions/player"}
	for _, tc := range []struct {
		name, session, token, tenant string
		expired                      bool
		status                       string
	}{
		{"success-no-tenant", ticket.userName, "private-match-token", "", false, "0"},
		{"current-alias", strings.Replace(ticket.userName, "tenants/"+labTenant+"/", "tenants/current/", 1), "private-match-token", "", false, "0"},
		{"participant-id", "player", "private-match-token", "", false, "0"},
		{"relative-participant", "userSessions/player", "private-match-token", "", false, "0"},
		{"relative-room-participant", "gameSessions/room/userSessions/player", "private-match-token", "", false, "0"},
		{"relative-other-room", "gameSessions/other-room/userSessions/player", "private-match-token", "", false, "16"},
		{"relative-other-participant", "userSessions/other-player", "private-match-token", "", false, "16"},
		{"other-participant-id", "other-player", "private-match-token", "", false, "16"},
		{"other-room-same-participant", strings.Replace(ticket.userName, "/room/", "/other-room/", 1), "private-match-token", "", false, "16"},
		{"other-tenant-same-participant", strings.Replace(ticket.userName, labTenant, "other-tenant", 1), "private-match-token", "", false, "16"},
		{"unknown-token", ticket.userName, "forged-token", "", false, "16"},
		{"other-session", ticket.userName + "-other", "private-match-token", "", false, "16"},
		{"expired", ticket.userName, "private-match-token", "", true, "16"},
		{"foreign-tenant", ticket.userName, "private-match-token", "other", false, "3"},
		{"missing-token", ticket.userName, "", "", false, "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTicketStore()
			now := time.Now()
			if tc.expired {
				now = now.Add(-2 * time.Hour)
			}
			s.rememberMatchToken(ticket, "private-match-token", now)
			payload := protoBytes(nil, 1, []byte(tc.session))
			payload = protoBytes(payload, 2, []byte(tc.token))
			var frame bytes.Buffer
			// Build a gRPC frame using the same writer as the server.
			framed := httptest.NewRecorder()
			_ = writeGRPCFrame(framed, payload)
			frame.Write(framed.Body.Bytes())
			req := httptest.NewRequest("POST", gamesyncIssueTokenPath, &frame)
			req.Header.Set("Content-Type", "application/grpc")
			if tc.tenant != "" {
				req.Header.Set("npln-tenant-id", tc.tenant)
			}
			var logs bytes.Buffer
			rr := httptest.NewRecorder()
			s.issueGamesyncToken(a, log.New(&logs, "", 0))(rr, req)
			res := rr.Result()
			res.Body.Close()
			if res.Trailer.Get("Grpc-Status") != tc.status {
				t.Fatalf("Status %s, expected %s", res.Trailer.Get("Grpc-Status"), tc.status)
			}
			if strings.Contains(logs.String(), "private-match-token") || strings.Contains(logs.String(), "forged-token") {
				t.Fatal("A token was logged")
			}
			if tc.status != "0" {
				return
			}
			var tokenBytes []byte
			_ = visitProto(rr.Body.Bytes()[5:], func(f, w, _ uint64, v []byte) error {
				if f == 1 && w == 2 {
					tokenBytes = v
				}
				return nil
			})
			var session, access, refresh string
			var ttl []byte
			_ = visitProto(tokenBytes, func(f, w, _ uint64, v []byte) error {
				if w == 2 {
					switch f {
					case 1:
						session = string(v)
					case 2:
						access = string(v)
					case 3:
						refresh = string(v)
					case 4:
						ttl = v
					}
				}
				return nil
			})
			claims, valid := a.verifyJWT(access)
			seconds, _, err := protoTimeParts(ttl)
			if !valid || claims.Subject != ticket.owner || session != tc.session || len(refresh) != 48 || err != nil || seconds != 8*3600 {
				t.Fatal("Gamesync token incomplete or unverifiable")
			}
			if strings.Contains(logs.String(), access) || strings.Contains(logs.String(), refresh) {
				t.Fatal("Private response logged")
			}
		})
	}
}
