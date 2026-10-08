package main

import (
	"encoding/base64"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Nextendo's public reference retains a second Duration in Heartbeat:
// 30-second interval and 50-second deadline. The older community schema names
// only the first field; preserve the complete wire shape.
func localPresenceHeartbeat() []byte {
	b := protoBytes(nil, 1, protoVarint(nil, 1, 30))
	return protoBytes(b, 2, protoVarint(nil, 1, 50))
}

// Enumeration cursor with Nextendo's published shape. Contains only authorized
// users actually present in this batch. It is not an authentication token and
// grants no permissions; resumption remains pending.
func localPresenceEnumerationCursor(uids []string) string {
	var raw []byte
	for _, uid := range uids {
		entry := protoBytes(nil, 1, []byte(uid))
		entry = protoBytes(entry, 2, nil)
		raw = protoBytes(raw, 1, entry)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

type localPresence struct {
	name         string
	state        uint64
	attrs        map[string][]byte // Original protobuf entry without interpreting its value.
	lastOnline   []byte
	unsubscribed bool
	debugInfo    []byte
}

// friends/v1/resources.proto: the server derives state from the KeepAlive channel.
const (
	localPresenceOnline  uint64 = 1
	localPresenceOffline uint64 = 2
)

func localPresenceName(uid string) string {
	return "tenants/" + labTenant + "/users/" + uid + "/presence"
}

type localPresenceStore struct {
	mu      sync.Mutex
	entries map[string]localPresence
	leases  map[string]map[uint64]bool
	next    uint64
	changed chan struct{}
}

func newLocalPresenceStore() *localPresenceStore {
	return &localPresenceStore{entries: make(map[string]localPresence), leases: make(map[string]map[uint64]bool), changed: make(chan struct{})}
}
func (s *localPresenceStore) notify() { close(s.changed); s.changed = make(chan struct{}) }
func (s *localPresenceStore) open(uid string) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	if s.leases[uid] == nil {
		s.leases[uid] = make(map[uint64]bool)
	}
	s.leases[uid][s.next] = true
	if len(s.leases[uid]) == 1 {
		p := s.entries[uid]
		p.name = localPresenceName(uid)
		p.state = localPresenceOnline
		p.lastOnline = nil
		if p.attrs == nil {
			p.attrs = make(map[string][]byte)
		}
		s.entries[uid] = p
		s.notify()
	}
	return s.next
}
func (s *localPresenceStore) close(uid string, lease uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.leases[uid][lease] {
		return
	}
	delete(s.leases[uid], lease)
	if len(s.leases[uid]) == 0 {
		delete(s.leases, uid)
		if p, ok := s.entries[uid]; ok {
			p.state = localPresenceOffline
			p.lastOnline = protoTimestamp(time.Now())
			s.entries[uid] = p
			s.notify()
		}
	}
}
func parsePresence(data []byte) (localPresence, error) {
	p := localPresence{attrs: make(map[string][]byte)}
	err := visitProto(data, func(f, w, n uint64, v []byte) error {
		switch f {
		case 1:
			if w != 2 {
				return errors.New("Invalid name")
			}
			p.name = string(v)
		case 2:
			if w != 0 || n > 3 {
				return errors.New("Invalid state")
			}
			p.state = n
		case 3:
			if w != 2 {
				return errors.New("Invalid attribute")
			}
			key, err := protoStringField1(v)
			if err != nil {
				return err
			}
			p.attrs[key] = append([]byte(nil), v...)
		case 4:
			if w != 2 {
				return errors.New("Invalid date")
			}
			p.lastOnline = append([]byte(nil), v...)
		case 5:
			if w != 0 || n > 1 {
				return errors.New("Invalid unsubscribed")
			}
			p.unsubscribed = n == 1
		case 6:
			if w != 2 {
				return errors.New("Invalid debug_info")
			}
			p.debugInfo = append([]byte(nil), v...)
		}
		return nil
	})
	return p, err
}
func presenceMask(data []byte) ([]string, error) {
	var paths []string
	err := visitProto(data, func(f, w, _ uint64, v []byte) error {
		if f == 1 {
			if w != 2 {
				return errors.New("Invalid mask")
			}
			paths = append(paths, string(v))
		}
		return nil
	})
	for _, path := range paths {
		if path != "*" && path != "name" && path != "state" && path != "attributes" && path != "last_online_time" && path != "unsubscribed" && path != "debug_info" && path != "debug_info.debug" && !(strings.HasPrefix(path, "attributes.") && len(path) > 11) {
			return nil, errors.New("Presence mask not implemented")
		}
	}
	return paths, err
}
func (s *localPresenceStore) update(uid string, lease uint64, payload []byte) error {
	var data, mask []byte
	err := visitProto(payload, func(f, w, _ uint64, v []byte) error {
		if f == 1 && w == 2 {
			return visitProto(v, func(f, w, _ uint64, v []byte) error {
				if f == 1 {
					data = v
				}
				if f == 2 {
					mask = v
				}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return err
	}
	p, err := parsePresence(data)
	if err != nil {
		return err
	}
	paths, err := presenceMask(mask)
	if err != nil {
		return err
	}
	// Name and connection belong to the server, not to the update.
	p.name = localPresenceName(uid)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.leases[uid][lease] {
		return errors.New("Presence channel closed")
	}
	old, ok := s.entries[uid]
	if !ok || len(paths) == 0 || containsPresencePath(paths, "*") {
		s.entries[uid] = p
	} else {
		for _, path := range paths {
			switch path {
			case "name":
				old.name = p.name
			case "state":
				old.state = p.state
			case "attributes":
				old.attrs = p.attrs
			case "last_online_time":
				old.lastOnline = p.lastOnline
			case "unsubscribed":
				old.unsubscribed = p.unsubscribed
			case "debug_info", "debug_info.debug":
				old.debugInfo = p.debugInfo
			default:
				key := strings.TrimPrefix(path, "attributes.")
				if value, ok := p.attrs[key]; ok {
					old.attrs[key] = value
				} else {
					delete(old.attrs, key)
				}
			}
		}
		s.entries[uid] = old
	}
	current := s.entries[uid]
	current.name = localPresenceName(uid)
	current.state = localPresenceOnline
	current.lastOnline = nil
	s.entries[uid] = current
	s.notify()
	return nil
}
func containsPresencePath(paths []string, path string) bool {
	for _, p := range paths {
		if p == path {
			return true
		}
	}
	return false
}
func encodePresence(p localPresence, mask []string) []byte {
	all := len(mask) == 0 || containsPresencePath(mask, "*")
	b := protoBytes(nil, 1, []byte(p.name))
	if all || containsPresencePath(mask, "state") {
		b = protoVarint(b, 2, p.state)
	}
	keys := make([]string, 0, len(p.attrs))
	for key := range p.attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if all || containsPresencePath(mask, "attributes") || containsPresencePath(mask, "attributes."+key) {
			b = protoBytes(b, 3, p.attrs[key])
		}
	}
	if len(p.lastOnline) > 0 && (all || containsPresencePath(mask, "last_online_time")) {
		b = protoBytes(b, 4, p.lastOnline)
	}
	if p.unsubscribed && (all || containsPresencePath(mask, "unsubscribed")) {
		b = protoVarint(b, 5, 1)
	}
	if len(p.debugInfo) > 0 && (all || containsPresencePath(mask, "debug_info") || containsPresencePath(mask, "debug_info.debug")) {
		b = protoBytes(b, 6, p.debugInfo)
	}
	return b
}
func presenceUID(name string, caller string) string {
	name = strings.TrimSuffix(name, "/presence")
	if name == "tenants/current/users/current" {
		return caller
	}
	for _, prefix := range []string{"tenants/" + labTenant + "/users/", "tenants/current/users/"} {
		if strings.HasPrefix(name, prefix) {
			uid := strings.TrimPrefix(name, prefix)
			if uid != "" && !strings.Contains(uid, "/") {
				return uid
			}
		}
	}
	return ""
}
func (a *labAuth) subscribeLocalPresences(w http.ResponseWriter, r *http.Request, uid string, payload []byte) {
	var requested []string
	var mask []string
	var maximum uint64
	var resume bool
	err := visitProto(payload, func(f, w, n uint64, v []byte) error {
		switch f {
		case 2:
			if w != 2 {
				return errors.New("Invalid destination")
			}
			requested = append(requested, string(v))
		case 3:
			if w != 2 {
				return errors.New("Invalid mask")
			}
			var err error
			mask, err = presenceMask(v)
			return err
		case 4:
			// Protobuf may sign-extend int32 -1 to 64 bits.
			// For this two-client pair, accept it as an unlimited bound.
			if w != 0 || (n > 1<<31-1 && n != ^uint64(0) && n != 1<<32-1) {
				return errors.New("Invalid limit")
			}
			if n <= 1<<31-1 {
				maximum = n
			}
		case 5:
			if w != 2 {
				return errors.New("Invalid cursor")
			}
			resume = len(v) > 0
		}
		return nil
	})
	if err != nil {
		a.logPresenceRejection(r, payload, err)
		grpcStatus(w, "3", "Invalid presence query", nil)
		return
	}
	if resume {
		grpcStatus(w, "12", "Presence resumption not implemented", nil)
		return
	}
	peers, _, known := a.friendSnapshot(r, uid)
	if !known {
		grpcStatus(w, "7", "User outside the local pair", nil)
		return
	}
	allowed := map[string]bool{uid: true}
	for _, peer := range peers {
		allowed[peer.uid] = true
	}
	targets := make(map[string]bool)
	if len(requested) == 0 {
		for _, peer := range peers {
			targets[peer.uid] = true
		}
	} else {
		for _, name := range requested {
			target := presenceUID(name, uid)
			if allowed[target] {
				targets[target] = true
			}
		}
	}
	if maximum > 0 && uint64(len(targets)) > maximum {
		grpcStatus(w, "8", "Insufficient presence limit", nil)
		return
	}
	if a.logger != nil {
		a.logger.Printf("Presence local: client=%q requested=%d authorized=%d mask=%d mask_paths=%q", r.RemoteAddr, len(requested), len(targets), len(mask), mask)
	}
	if a.identityDiagnostic != nil {
		a.identityDiagnostic.record(r, "presence", uid, requested)
	}
	heartbeat := protoBytes(nil, 3, localPresenceHeartbeat())
	if writeGRPCFrame(w, heartbeat) != nil {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	first := true
	for {
		if a.nextendo != nil {
			if _, ok := a.authorizedUser(w, r); !ok {
				return
			}
		}
		peers, friendChanged, _ := a.friendSnapshot(r, uid)
		allowed = map[string]bool{uid: true}
		for _, peer := range peers {
			allowed[peer.uid] = true
		}
		targets = make(map[string]bool)
		if len(requested) == 0 {
			for _, peer := range peers {
				targets[peer.uid] = true
			}
		} else {
			for _, name := range requested {
				target := presenceUID(name, uid)
				if allowed[target] {
					targets[target] = true
				}
			}
		}
		a.presences.mu.Lock()
		changed := a.presences.changed
		keys := make([]string, 0, len(targets))
		for target := range targets {
			keys = append(keys, target)
		}
		sort.Strings(keys)
		var list []byte
		var listedUsers []string
		count := 0
		for _, target := range keys {
			p, ok := a.presences.entries[target]
			if !ok {
				p = localPresence{name: localPresenceName(target), state: localPresenceOffline}
			}
			if a.logger != nil {
				attributesSelected := 0
				all := len(mask) == 0 || containsPresencePath(mask, "*")
				for key := range p.attrs {
					if all || containsPresencePath(mask, "attributes") || containsPresencePath(mask, "attributes."+key) {
						attributesSelected++
					}
				}
				a.logger.Printf("Presence structure: client=%q name_has_presence_suffix=%t state=%d state_source=server state_included=%t stored_attributes=%d selected_attributes=%d date_included=%t", r.RemoteAddr, strings.HasSuffix(p.name, "/presence"), p.state, all || containsPresencePath(mask, "state"), len(p.attrs), attributesSelected, len(p.lastOnline) > 0 && (all || containsPresencePath(mask, "last_online_time")))
			}
			list = protoBytes(list, 1, encodePresence(p, mask))
			listedUsers = append(listedUsers, target)
			count++
		}
		a.presences.mu.Unlock()
		if count > 0 {
			list = protoBytes(list, 2, []byte(localPresenceEnumerationCursor(listedUsers)))
			if writeGRPCFrame(w, protoBytes(nil, 1, list)) != nil {
				return
			}
		}
		if first {
			if writeGRPCFrame(w, protoBytes(nil, 2, nil)) != nil {
				return
			}
			first = false
		}
		if a.logger != nil {
			a.logger.Printf("Presence local: client=%q presences_sent=%d cursor_sent=%t heartbeat_deadline=50s", r.RemoteAddr, count, count > 0)
		}
		waiting := true
		for waiting {
			select {
			case <-r.Context().Done():
				return
			case <-changed:
				waiting = false
			case <-friendChanged:
				waiting = false
			case <-ticker.C:
				if writeGRPCFrame(w, heartbeat) != nil {
					return
				}
			}
		}
	}
}
