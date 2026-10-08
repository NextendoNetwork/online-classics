package main

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type accountActivity struct {
	PID      uint64
	Count    int
	LastRead time.Time
}
type classicsPresence struct {
	mu       sync.Mutex
	live     map[uint64]accountActivity
	snapshot atomic.Value
}

func newClassicsPresence() *classicsPresence {
	p := &classicsPresence{live: map[uint64]accountActivity{}}
	p.snapshot.Store([]accountActivity{})
	return p
}
func (p *classicsPresence) update(pid uint64, delta int, touch bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	activity := p.live[pid]
	activity.PID = pid
	activity.Count += delta
	if touch || activity.LastRead.IsZero() {
		activity.LastRead = time.Now()
	}
	if activity.Count <= 0 {
		delete(p.live, pid)
	} else {
		p.live[pid] = activity
	}
	current := make([]accountActivity, 0, len(p.live))
	for _, v := range p.live {
		current = append(current, v)
	}
	p.snapshot.Store(current)
}
func (a *labAuth) trackAccountStream(sid string, body io.ReadCloser) (io.ReadCloser, func()) {
	if a.nextendo == nil || a.nextendo.presence == nil {
		return body, func() {}
	}
	session, ok := a.nextendo.get(sid)
	if !ok {
		return body, func() {}
	}
	presence, pid := a.nextendo.presence, session.identity.PID
	presence.update(pid, 1, true)
	return &activityReadCloser{ReadCloser: body, presence: presence, pid: pid}, func() { presence.update(pid, -1, false) }
}

type activityReadCloser struct {
	io.ReadCloser
	presence *classicsPresence
	pid      uint64
}

func (r *activityReadCloser) Read(b []byte) (int, error) {
	n, e := r.ReadCloser.Read(b)
	if n > 0 {
		r.presence.update(r.pid, 0, true)
	}
	return n, e
}

// The account service calls this while an auth request is waiting on its gate.
// Reading an immutable snapshot avoids a circular lock dependency.
func (p *classicsPresence) handler(secret string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/api/stats" {
			http.NotFound(w, r)
			return
		}
		if len(secret) < 32 || subtle.ConstantTimeCompare([]byte(secret), []byte(r.URL.Query().Get("key"))) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		players := make([]map[string]any, 0)
		for _, v := range p.snapshot.Load().([]accountActivity) {
			idle := int64(time.Since(v.LastRead) / time.Second)
			players = append(players, map[string]any{"pid": v.PID, "idleSeconds": idle})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]any{"game": nextendoTitleID, "players": players})
	})
}
