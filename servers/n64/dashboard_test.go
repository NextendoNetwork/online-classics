package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"
	commonpb "npln.nintendo.net/npln-practice/proto/common"
	mmpb "npln.nintendo.net/npln-practice/proto/matchmaking/v1"
)

func dashboardFixture(t *testing.T) *gamesyncServer {
	t.Helper()
	retenirIdentite("users/u-host", 1800000001)
	retenirIdentite("users/u-guest", 1800000002)

	registry := newSessionRegistry()
	registry.sessions["rooms/r1"] = &mmpb.GameSession{
		Name:                "rooms/r1",
		State:               mmpb.GameSession_ACTIVE,
		MaxParticipantCount: 4,
		CreateTime:          timestamppb.Now(),
		Properties: &commonpb.MapValue{Fields: map[string]*commonpb.Value{
			"_BaseConfigName": {ValueType: &commonpb.Value_StringValue{StringValue: "MarioKart64"}},
		}},
		UserSessions: []*mmpb.UserSession{
			{User: "users/u-host"},
			{User: "users/u-guest"},
		},
	}

	g := newGamesyncServer(registry)
	g.sessions["us-1"] = gamesyncSession{UID: "u-host"}
	g.sessions["us-2"] = gamesyncSession{UID: "u-guest"}
	g.streamCounts = map[string]int{"us-1": 1, "us-2": 1}
	return g
}

func TestDashboardStatsShape(t *testing.T) {
	stats := dashboardFixture(t).dashboardStats()

	for _, key := range []string{
		"serverTime", "uptimeSeconds", "connected", "inLobby", "activeLobbies",
		"peakConnected", "totalSessions", "players", "gatherings", "server",
	} {
		if _, ok := stats[key]; !ok {
			t.Fatalf("missing top-level field %q", key)
		}
	}

	if got := stats["activeLobbies"].(int); got != 1 {
		t.Fatalf("activeLobbies = %d, want 1", got)
	}
	if got := stats["inLobby"].(int); got != 2 {
		t.Fatalf("inLobby = %d, want 2", got)
	}

	gatherings := stats["gatherings"].([]map[string]any)
	if len(gatherings) != 1 {
		t.Fatalf("gatherings = %d, want 1", len(gatherings))
	}
	room := gatherings[0]
	for _, key := range []string{"id", "hostPid", "hostName", "mode", "count", "max", "state", "players"} {
		if _, ok := room[key]; !ok {
			t.Fatalf("gathering missing %q", key)
		}
	}
	if room["mode"] != "MarioKart64" {
		t.Fatalf("mode = %v, want MarioKart64", room["mode"])
	}
	if room["hostPid"].(uint64) != 1800000001 {
		t.Fatalf("hostPid = %v, want 1800000001", room["hostPid"])
	}
	if room["max"].(int) != 4 {
		t.Fatalf("max = %v, want 4", room["max"])
	}

	players := stats["players"].([]map[string]any)
	if len(players) != 2 {
		t.Fatalf("players = %d, want 2", len(players))
	}
	for _, key := range []string{"pid", "name", "ip", "state", "gathering", "onlineSeconds", "idleSeconds", "isHost"} {
		if _, ok := players[0][key]; !ok {
			t.Fatalf("player missing %q", key)
		}
	}
	if players[0]["gathering"].(uint32) != room["id"].(uint32) {
		t.Fatalf("player gathering %v != room id %v", players[0]["gathering"], room["id"])
	}
	if stats["server"].(map[string]any)["accessKey"] != nplnTenantID {
		t.Fatalf("accessKey = %v, want %s", stats["server"].(map[string]any)["accessKey"], nplnTenantID)
	}
}

func TestDashboardAuth(t *testing.T) {
	g := dashboardFixture(t)
	t.Setenv("DASH_TOKEN", "correct-horse")
	handler := dashboardHandler(g)

	for _, tc := range []struct {
		name string
		key  string
		want int
	}{
		{"wrong key", "nope", http.StatusUnauthorized},
		{"missing key", "", http.StatusUnauthorized},
		{"correct key", "correct-horse", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stats?key="+tc.key, nil))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.want == http.StatusOK {
				var body map[string]any
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("response is not JSON: %v", err)
				}
				if _, ok := body["connected"]; !ok {
					t.Fatal("decoded body missing connected")
				}
			}
		})
	}
}

// An unset DASH_TOKEN must refuse everything rather than accept an empty key.
func TestDashboardRefusesWhenTokenUnset(t *testing.T) {
	t.Setenv("DASH_TOKEN", "")
	rec := httptest.NewRecorder()
	dashboardHandler(dashboardFixture(t)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/stats?key=", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
