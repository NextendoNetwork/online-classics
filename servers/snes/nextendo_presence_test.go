package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAccountPresenceUsesLiveStreamsAndDoesNotLockAuth(t *testing.T) {
	p := newClassicsPresence()
	secret := strings.Repeat("s", 32)
	h := p.handler(secret)
	p.update(123, 1, true)
	p.update(123, 1, true)
	// Holding the writer lock models an account request in progress. The stats
	// request must still finish, because online-check may call it synchronously.
	p.mu.Lock()
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/stats?key="+secret, nil))
		done <- w
	}()
	var response *httptest.ResponseRecorder
	select {
	case response = <-done:
	case <-time.After(time.Second):
		p.mu.Unlock()
		t.Fatal("presence caused circular auth lock")
	}
	p.mu.Unlock()
	var stats struct {
		Players []struct {
			PID         uint64
			IdleSeconds int64
		}
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &stats) != nil || len(stats.Players) != 1 || stats.Players[0].PID != 123 {
		t.Fatal("canonical account not published once")
	}
	p.update(123, -1, false)
	if len(p.snapshot.Load().([]accountActivity)) != 1 {
		t.Fatal("closing one stream released another")
	}
	p.update(123, -1, false)
	if len(p.snapshot.Load().([]accountActivity)) != 0 {
		t.Fatal("last stream left ghost account")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/stats?key=wrong", nil))
	if w.Code != 401 {
		t.Fatal("private stats leaked")
	}
}
