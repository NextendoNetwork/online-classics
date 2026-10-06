package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccountPresenceReportsVerifiedPIDAndTitle(t *testing.T) {
	t.Setenv("NEXTENDO_INTERNAL_KEY", "test-internal")
	old := accountBaseURL
	defer func() { accountBaseURL = old }()
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/internal/presence-batch" || r.Method != "POST" || r.Header.Get("X-Internal-Key") != "test-internal" {
			t.Error("wrong authenticated request")
		}
		var body struct {
			Status   int
			AppID    string   `json:"appId"`
			PIDs     []uint64 `json:"pids"`
			Platform string
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Status != 2 || body.AppID != "0100C9A00ECE6000" || len(body.PIDs) != 1 || body.PIDs[0] != 123 || body.Platform != "" {
			t.Errorf("incorrect presence %+v", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer s.Close()
	accountBaseURL = s.URL
	if err := reportAccountPresence(context.Background(), 123); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := reportAccountPresence(ctx, 123); err == nil {
		t.Fatal("cancelled report succeeded")
	}
	if calls != 1 {
		t.Fatalf("calls=%d", calls)
	}
	if err := reportAccountPresence(context.Background(), 0); err == nil {
		t.Fatal("zero PID accepted")
	}
}
