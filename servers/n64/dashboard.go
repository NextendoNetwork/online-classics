package main

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	mmpb "npln.nintendo.net/npln-practice/proto/matchmaking/v1"
)

var (
	dashboardOnce  sync.Once
	dashboardStart = time.Now()

	dashboardMu   sync.Mutex
	dashboardPeak int
	dashboardSeen = map[uint64]bool{}
)

// startGameDashboard serves the /api/stats contract the other Nextendo game servers expose.
// DASH_LISTEN must be a bridge address such as 10.0.1.1:8107, never a public one.
func startGameDashboard(g *gamesyncServer) {
	addr := os.Getenv("DASH_LISTEN")
	if addr == "" {
		return
	}
	dashboardOnce.Do(func() {
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/api/stats", dashboardHandler(g))
			log.Printf("[N64 Dashboard] listening on %s", addr)
			if err := http.ListenAndServe(addr, mux); err != nil {
				log.Printf("[N64 Dashboard] %v", err)
			}
		}()
	})
}

func dashboardHandler(g *gamesyncServer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := os.Getenv("DASH_TOKEN")
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(r.URL.Query().Get("key"))) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(g.dashboardStats())
	})
}

// roomMode labels a room by the config the host published.
func roomMode(gs *mmpb.GameSession) string {
	if base := gs.GetProperties().GetFields()["_BaseConfigName"].GetStringValue(); base != "" {
		return base
	}
	return "Online lobby"
}

func (g *gamesyncServer) dashboardStats() map[string]any {
	connected := map[string]bool{}
	g.mu.RLock()
	for id, s := range g.sessions {
		if g.streamCounts[id] > 0 {
			connected[s.UID] = true
		}
	}
	g.mu.RUnlock()

	rooms := []*mmpb.GameSession{}
	if g.registry != nil {
		g.registry.mu.Lock()
		for _, gs := range g.registry.sessions {
			if gs.GetState() == mmpb.GameSession_ACTIVE {
				rooms = append(rooms, proto.Clone(gs).(*mmpb.GameSession))
			}
		}
		g.registry.mu.Unlock()
	}
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].GetName() < rooms[j].GetName() })

	players := []map[string]any{}
	gatherings := []map[string]any{}
	seen := map[uint64]bool{}
	inLobby := 0

	for _, gs := range rooms {
		h := fnv.New32a()
		h.Write([]byte(gs.GetName()))
		id := h.Sum32()

		members := []map[string]any{}
		var hostPid uint64
		var hostName string
		for i, us := range gs.GetUserSessions() {
			pid := pidPourUid(userIDFromPath(us.GetUser()))
			if pid == 0 {
				continue
			}
			name := fmt.Sprintf("Player-%d", pid)
			isHost := i == 0
			if isHost {
				hostPid, hostName = pid, name
			}
			members = append(members, map[string]any{"pid": pid, "name": name, "host": isHost})
			if seen[pid] {
				continue
			}
			seen[pid] = true
			players = append(players, map[string]any{
				"pid": pid, "name": name, "ip": "", "state": "in a lobby",
				"gathering": id, "isHost": isHost, "idleSeconds": 0,
				"onlineSeconds": int(time.Since(gs.GetCreateTime().AsTime()).Seconds()),
			})
		}
		if len(members) == 0 {
			continue
		}
		inLobby += len(members)

		max := int(gs.GetMaxParticipantCount())
		if max < len(members) {
			max = len(members)
		}
		mode := roomMode(gs)
		gatherings = append(gatherings, map[string]any{
			"id": id, "hostPid": hostPid, "hostName": hostName,
			"mode": mode, "type": mode, "state": "active",
			"count": len(members), "max": max, "players": members,
		})
	}

	// Connected but in no room: the player is sitting in the online menus.
	uids := make([]string, 0, len(connected))
	for uid := range connected {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	for _, uid := range uids {
		pid := pidPourUid(uid)
		if pid == 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		players = append(players, map[string]any{
			"pid": pid, "name": fmt.Sprintf("Player-%d", pid), "ip": "", "state": "online",
			"gathering": uint32(0), "isHost": false, "idleSeconds": 0, "onlineSeconds": 0,
		})
	}

	// Peak and total are sampled per poll; the server keeps no session history of its own.
	dashboardMu.Lock()
	if len(players) > dashboardPeak {
		dashboardPeak = len(players)
	}
	for pid := range seen {
		dashboardSeen[pid] = true
	}
	peak, total := dashboardPeak, len(dashboardSeen)
	dashboardMu.Unlock()

	return map[string]any{
		"serverTime":    time.Now().Format("15:04:05"),
		"uptimeSeconds": int(time.Since(dashboardStart).Seconds()),
		"connected":     len(players),
		"inLobby":       inLobby,
		"activeLobbies": len(gatherings),
		"peakConnected": peak,
		"totalSessions": total,
		"players":       players,
		"gatherings":    gatherings,
		"events":        []any{},
		"methods":       []any{},
		"server": map[string]any{
			"accessKey": nplnTenantID, "stack": "npln", "nexVersion": "NPLN", "sniHost": nplnTLSHostname,
		},
	}
}
