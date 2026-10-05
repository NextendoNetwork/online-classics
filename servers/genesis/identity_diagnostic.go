package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// Optional diagnostic for preparing an explicit mapping of the two clients.
// Contains only IPs, user IDs and friend resource names; never credentials,
// external tokens, JWTs or game properties.
type identityDiagnostic struct {
	mu  sync.Mutex
	out io.Writer
}

func (d *identityDiagnostic) record(r *http.Request, kind, uid string, users []string) {
	if d == nil {
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = "unknown"
	}
	event := struct {
		Time   string   `json:"time"`
		Kind   string   `json:"kind"`
		IP     string   `json:"client_ip"`
		User   string   `json:"local_user"`
		Wanted []string `json:"wanted_users,omitempty"`
	}{time.Now().UTC().Format(time.RFC3339), kind, ip, uid, users}
	d.mu.Lock()
	defer d.mu.Unlock()
	// Diagnostic failure grants no permissions and does not change the RPC response.
	_ = json.NewEncoder(d.out).Encode(event)
}
