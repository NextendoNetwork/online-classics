package main

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const listFriendUsersPath = "/nn.npln.friends.v1.Friends/ListFriendUsers"

type localFriendPeer struct {
	uid, nsa   string
	registered bool
}
type localFriendPair struct {
	mu                    sync.Mutex
	peers                 map[string]localFriendPeer // Source IP -> local test identity.
	changed               chan struct{}
	loopbackByDestination bool // Two local emulators: socket destination IP.
}

func localFriendIPKey(ip net.IP) string {
	if ip.IsLoopback() && ip.To4() == nil {
		return "127.0.0.1"
	}
	return ip.String()
}

// Explicit lab mode: one pair of two private/loopback IPs under the tester's control.
// Does not import Nextendo's friend graph or authenticate production accounts.
func newLocalFriendPair(config string) (*localFriendPair, error) {
	if config == "" {
		return nil, nil
	}
	parts := strings.Split(config, ",")
	if len(parts) != 2 {
		return nil, errors.New("Specify exactly two test-client IPs")
	}
	p := &localFriendPair{peers: make(map[string]localFriendPeer), changed: make(chan struct{})}
	for _, part := range parts {
		ip := net.ParseIP(strings.TrimSpace(part))
		if ip == nil || (!ip.IsLoopback() && (ip.To4() == nil || !ip.IsPrivate())) {
			return nil, errors.New("The pair accepts only private or loopback IPs")
		}
		key := localFriendIPKey(ip)
		if _, exists := p.peers[key]; exists {
			return nil, errors.New("Clients must have different IPs")
		}
		identity := sha256.Sum256([]byte("genesis-lab-peer:" + key))
		label := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(identity[:13]))
		p.peers[key] = localFriendPeer{uid: "u-" + label, nsa: hex.EncodeToString(identity[:8])}
	}
	p.loopbackByDestination = true
	for key := range p.peers {
		if !net.ParseIP(key).IsLoopback() {
			p.loopbackByDestination = false
		}
	}
	return p, nil
}

// Maps tester-supplied NSA IDs to the two lab clients.
// These are identifiers, not credentials; they do not verify Nextendo accounts.
func (p *localFriendPair) setNSAOverrides(config string) error {
	if config == "" {
		return nil
	}
	if p == nil {
		return errors.New("lab-friend-nsa requires lab-friend-clients")
	}
	parts := strings.Split(config, ",")
	if len(parts) != 2 {
		return errors.New("Specify IP=NSA for both clients")
	}
	overrides := make(map[string]string)
	seen := make(map[string]bool)
	for _, part := range parts {
		pair := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(pair) != 2 {
			return errors.New("Use IP=NSA")
		}
		ip := net.ParseIP(strings.TrimSpace(pair[0]))
		if ip == nil {
			return errors.New("Invalid IP")
		}
		key := localFriendIPKey(ip)
		value := strings.ToLower(strings.TrimSpace(pair[1]))
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 8 || value == "0000000000000000" {
			return errors.New("NSA must contain 16 hexadecimal digits and be nonzero")
		}
		if _, ok := p.peers[key]; !ok {
			return errors.New("IP outside the configured pair")
		}
		if _, ok := overrides[key]; ok || seen[value] {
			return errors.New("Clients and NSA IDs must be distinct")
		}
		overrides[key] = value
		seen[value] = true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, value := range overrides {
		peer := p.peers[key]
		peer.nsa = value
		p.peers[key] = peer
	}
	return nil
}

func (p *localFriendPair) peerFor(r *http.Request) (string, localFriendPeer, bool) {
	if p == nil {
		return "", localFriendPeer{}, false
	}
	ipText, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "", localFriendPeer{}, false
	}
	ip := net.ParseIP(ipText)
	if ip == nil {
		return "", localFriendPeer{}, false
	}
	key := ip.String()
	if ip.IsLoopback() {
		key = "127.0.0.1"
	}
	if p.loopbackByDestination {
		// Two processes may share a localhost source address. Only for an explicit
		// pair of loopback destinations, use the destination confirmed by the server
		// socket; Host and forwarded headers do not establish identity.
		if !ip.IsLoopback() {
			return "", localFriendPeer{}, false
		}
		addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
		if !ok || addr == nil {
			return "", localFriendPeer{}, false
		}
		localHost, _, err := net.SplitHostPort(addr.String())
		localIP := net.ParseIP(localHost)
		if err != nil || localIP == nil || !localIP.IsLoopback() {
			return "", localFriendPeer{}, false
		}
		key = localFriendIPKey(localIP)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	peer, ok := p.peers[key]
	return key, peer, ok
}

func (p *localFriendPair) register(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	peer, ok := p.peers[key]
	if !ok || peer.registered {
		return
	}
	peer.registered = true
	p.peers[key] = peer
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *localFriendPair) snapshot(uid string) ([]localFriendPeer, <-chan struct{}, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	known := false
	var peers []localFriendPeer
	for _, peer := range p.peers {
		if peer.uid == uid && peer.registered {
			known = true
		}
		if peer.uid != uid && peer.registered {
			peers = append(peers, peer)
		}
	}
	return peers, p.changed, known
}

func encodeLocalFriend(uid string, peer localFriendPeer) []byte {
	prefix := "tenants/" + labTenant + "/users/"
	b := protoBytes(nil, 1, []byte(prefix+uid+"/friendUsers/"+peer.uid))
	b = protoBytes(b, 2, []byte(prefix+peer.uid))
	b = protoBytes(b, 3, []byte(peer.nsa))
	return protoBytes(b, 4, protoVarint(protoVarint(nil, 2, 1), 3, 1))
}

func (p *localFriendPair) serveSubscription(w http.ResponseWriter, r *http.Request, uid string, logger *log.Logger) {
	interval := protoBytes(nil, 3, protoVarint(nil, 1, 50))
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		peers, changed, known := p.snapshot(uid)
		if !known {
			grpcStatus(w, "7", "User outside the local pair", nil)
			return
		}
		response := append([]byte(nil), interval...)
		for _, peer := range peers {
			account := protoBytes(nil, 1, []byte(peer.nsa))
			account = protoBytes(account, 2, encodeLocalFriend(uid, peer))
			response = protoBytes(response, 1, account)
		}
		if err := writeGRPCFrame(w, response); err != nil {
			if logger != nil {
				logger.Printf("Friends local: client=%q send_failed=true", r.RemoteAddr)
			}
			return
		}
		if logger != nil {
			logger.Printf("Friends local: client=%q friends_sent=%d bytes=%d", r.RemoteAddr, len(peers), len(response))
		}
		// One stream writer. Capture the revision with the snapshot to avoid losing
		// a registration between the snapshot and wait.
		waiting := true
		for waiting {
			select {
			case <-r.Context().Done():
				return
			case <-changed:
				waiting = false
			case <-ticker.C:
				if writeGRPCFrame(w, interval) != nil {
					return
				}
			}
		}
	}
}

func (a *labAuth) listLocalFriends(w http.ResponseWriter, r *http.Request) {
	if !validGRPC(w, r) {
		return
	}
	uid, ok := a.authorizedUser(w, r)
	if !ok {
		return
	}
	payload, err := grpcRequestPayload(r)
	if err != nil {
		grpcStatus(w, "3", "Invalid frame", nil)
		return
	}
	parent, err := protoStringField1(payload)
	if err != nil || !validUserPath(parent, uid) {
		grpcStatus(w, "3", "Incorrect user", nil)
		return
	}
	var cursor bool
	err = visitProto(payload, func(f, w, n uint64, v []byte) error {
		if f == 2 && (w != 0 || n > 1<<31-1) {
			return errors.New("Invalid page")
		}
		if f == 3 {
			if w != 2 {
				return errors.New("Invalid cursor")
			}
			cursor = len(v) > 0
		}
		return nil
	})
	if err != nil {
		grpcStatus(w, "3", "Invalid query", nil)
		return
	}
	if cursor {
		grpcStatus(w, "3", "No further page exists", nil)
		return
	}
	if a.friendPair == nil {
		grpcStatus(w, "0", "", nil)
		return
	}
	peers, _, known := a.friendPair.snapshot(uid)
	if !known {
		grpcStatus(w, "7", "User outside the local pair", nil)
		return
	}
	var b []byte
	for _, peer := range peers {
		b = protoBytes(b, 1, encodeLocalFriend(uid, peer))
	}
	grpcStatus(w, "0", "", b)
}
