package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	friendspb "npln.nintendo.net/npln-practice/proto/friends/v1"
	"os"
	"time"
)

// Mirror authenticated NPLN presence into the account friends list. The account
// service expires unrefreshed entries after 90 seconds; never announce offline
// here, since another live client or game may now own the account's presence.
func reportAccountPresence(ctx context.Context, pid uint64) error {
	if pid == 0 {
		return fmt.Errorf("missing account PID")
	}
	body, _ := json.Marshal(struct {
		Status int      `json:"status"`
		AppID  string   `json:"appId"`
		PIDs   []uint64 `json:"pids"`
	}{2, "0100C9A00ECE6000", []uint64{pid}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, accountBaseURL+"/internal/presence-batch", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("NEXTENDO_INTERNAL_KEY"); key != "" {
		req.Header.Set("X-Internal-Key", key)
	}
	resp, err := accountHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("account presence HTTP %d", resp.StatusCode)
	}
	return nil
}
func keepAccountPresence(ctx context.Context, uid string, generation uint64, pid uint64) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		livePresences.Lock()
		current := livePresences.generations[uid] == generation && livePresences.entries[uid] != nil && livePresences.entries[uid].GetState() == friendspb.State_ONLINE
		livePresences.Unlock()
		if !current {
			return
		}
		if err := reportAccountPresence(ctx, pid); err != nil && ctx.Err() == nil {
			log.Printf("[NPLN Presence] account report pid=%d failed: %v", pid, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
