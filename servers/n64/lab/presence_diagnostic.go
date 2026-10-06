package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type presenceLogLimiter struct {
	mu      sync.Mutex
	last    map[string]time.Time
	skipped map[string]int
}

func (l *presenceLogLimiter) allow(client string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		l.last = make(map[string]time.Time)
		l.skipped = make(map[string]int)
	}
	if time.Since(l.last[client]) < 5*time.Second {
		l.skipped[client]++
		return false, 0
	}
	if len(l.last) > 128 {
		clear(l.last)
		clear(l.skipped)
	}
	dropped := l.skipped[client]
	l.skipped[client] = 0
	l.last[client] = time.Now()
	return true, dropped
}

// Schema only: labels, types, limit and known field names.
// Does not expose attributes, requested IDs or tokens, even for invalid frames.
func presenceRequestShape(payload []byte) string {
	var fields []string
	_ = visitProto(payload, func(f, w, n uint64, v []byte) error {
		field := fmt.Sprintf("%d:%d", f, w)
		if f == 4 && w == 0 {
			field += fmt.Sprintf("(limite=%d)", n)
		}
		if f == 3 && w == 2 {
			var paths []string
			_ = visitProto(v, func(f, w, _ uint64, v []byte) error {
				if f == 1 && w == 2 {
					path := string(v)
					switch path {
					case "*", "name", "state", "attributes", "last_online_time", "unsubscribed", "debug_info", "debug_info.debug":
						paths = append(paths, path)
					default:
						paths = append(paths, fmt.Sprintf("otro(length=%d)", len(v)))
					}
				}
				return nil
			})
			field += "(mask=" + strings.Join(paths, ",") + ")"
		}
		fields = append(fields, field)
		return nil
	})
	return strings.Join(fields, " ")
}
func (a *labAuth) logPresenceRejection(r *http.Request, payload []byte, err error) {
	if a.logger == nil {
		return
	}
	a.presenceLogMu.Lock()
	defer a.presenceLogMu.Unlock()
	if a.presenceLogLast == nil {
		a.presenceLogLast = make(map[string]time.Time)
	}
	if time.Since(a.presenceLogLast[r.RemoteAddr]) < 30*time.Second {
		return
	}
	a.presenceLogLast[r.RemoteAddr] = time.Now()
	a.logger.Printf("Presence query rejected: client=%q reason=%q esquema=%q", r.RemoteAddr, err.Error(), presenceRequestShape(payload))
}
