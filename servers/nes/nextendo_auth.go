package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NextendoNetwork/online-classics/accountauth"
)

const refreshTokenPath = "/nn.npln.auth.v1.Auth/RefreshToken"

func splitBearer(r *http.Request) string {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}

type accountSession struct {
	identity accountauth.Identity
	kind     string
	expires  time.Time
}
type nextendoSessions struct {
	rooms    *ticketStore
	presence *classicsPresence
	verifier *accountauth.NextendoAuth
	identity func(uint64) (accountauth.Identity, error)
	mu       sync.Mutex
	sessions map[string]accountSession
	refresh  map[[32]byte]string
}

func remotePeerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func (s *nextendoSessions) enroll(token, ip string) (accountauth.Identity, string, error) {
	pid, err := s.verifier.Authenticate(token, ip)
	if err != nil {
		return accountauth.Identity{}, "", err
	}
	identity, err := s.identity(pid)
	if err != nil {
		return accountauth.Identity{}, "", err
	}
	return identity, s.verifier.DeviceKind(token), nil
}

// Expired sessions and their hashed refresh credentials are removed before
// insertion. Capacity is fixed; credentials cannot grow memory indefinitely.
func (s *nextendoSessions) store(identity accountauth.Identity, kind string) (string, string, error) {
	var raw [32]byte
	if _, e := rand.Read(raw[:]); e != nil {
		return "", "", e
	}
	refresh := hex.EncodeToString(raw[:])
	hash := sha256.Sum256([]byte(refresh))
	var sidRaw [16]byte
	if _, e := rand.Read(sidRaw[:]); e != nil {
		return "", "", e
	}
	sid := hex.EncodeToString(sidRaw[:])
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, v := range s.sessions {
		if !v.expires.After(now) {
			delete(s.sessions, id)
		}
	}
	for h, id := range s.refresh {
		if _, ok := s.sessions[id]; !ok {
			delete(s.refresh, h)
		}
	}
	if len(s.sessions) >= 4096 {
		return "", "", errors.New("session capacity reached")
	}
	s.sessions[sid] = accountSession{identity: identity, kind: kind, expires: now.Add(24 * time.Hour)}
	s.refresh[hash] = sid
	return sid, refresh, nil
}

func (s *nextendoSessions) get(sid string) (accountSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.sessions[sid]
	return v, ok && v.expires.After(time.Now())
}

func (a *labAuth) checkAccountSession(sid, uid, ip string) bool {
	v, ok := a.nextendo.get(sid)
	if !ok || v.identity.UserID != uid || !a.nextendo.verifier.CheckOnline(v.identity.PID, v.kind, ip) {
		return false
	}
	identity, err := a.nextendo.identity(v.identity.PID)
	return err == nil && identity.UserID == uid && identity.AccountHex == v.identity.AccountHex
}

func (a *labAuth) refreshAccountToken(w http.ResponseWriter, r *http.Request) {
	if !validGRPC(w, r) {
		return
	}
	if a.nextendo == nil {
		grpcStatus(w, "12", "Refresh requires Nextendo deployment", nil)
		return
	}
	payload, err := grpcRequestPayload(r)
	var user, refresh string
	if err == nil {
		err = visitProto(payload, func(f, wire, _ uint64, v []byte) error {
			if f == 1 || f == 2 {
				if wire != 2 {
					return errors.New("invalid refresh")
				}
				if f == 1 {
					user = string(v)
				} else {
					refresh = string(v)
				}
			}
			return nil
		})
	}
	if err != nil || len(refresh) != 64 {
		grpcStatus(w, "16", "Refresh rejected", nil)
		return
	}
	hash := sha256.Sum256([]byte(refresh))
	a.nextendo.mu.Lock()
	sid, exists := a.nextendo.refresh[hash]
	old := a.nextendo.sessions[sid]
	a.nextendo.mu.Unlock()
	if !exists || !validUserPath(user, old.identity.UserID) || !a.checkAccountSession(sid, old.identity.UserID, remotePeerIP(r)) {
		grpcStatus(w, "16", "Refresh rejected", nil)
		return
	}
	// Single-use rotation. A concurrent replay cannot mint another session.
	a.nextendo.mu.Lock()
	if a.nextendo.refresh[hash] != sid {
		a.nextendo.mu.Unlock()
		grpcStatus(w, "16", "Refresh rejected", nil)
		return
	}
	delete(a.nextendo.refresh, hash)
	a.nextendo.mu.Unlock()
	req := authRequest{tenant: "tenants/" + labTenant, externalType: 1, localPeerIdentity: old.identity.UserID, localPeerNSA: old.identity.AccountHex, accountPID: old.identity.PID, accountKind: old.kind, accountIdentity: old.identity}
	response, err := a.makeResponse(req)
	if err != nil {
		grpcStatus(w, "13", "Refresh unavailable", nil)
		return
	}
	// RefreshTokenResponse contains Token as field 1 (not User + Token).
	var token []byte
	_ = visitProto(response, func(f, w, _ uint64, v []byte) error {
		if f == 2 && w == 2 {
			token = append([]byte(nil), v...)
		}
		return nil
	})
	grpcStatus(w, "0", "", protoBytes(nil, 1, token))
}

func (a *labAuth) accountFriends(r *http.Request, uid string) ([]localFriendPeer, error) {
	parts := splitBearer(r)
	claims, ok := a.verifyJWT(parts)
	if !ok || claims.Subject != uid {
		return nil, errors.New("invalid account session")
	}
	v, ok := a.nextendo.get(claims.Session)
	if !ok {
		return nil, errors.New("expired session")
	}
	identity, err := a.nextendo.identity(v.identity.PID)
	if err != nil {
		return nil, err
	}
	peers := make([]localFriendPeer, 0, len(identity.Friends))
	for _, f := range identity.Friends {
		peers = append(peers, localFriendPeer{uid: f.UserID, nsa: f.AccountHex, registered: true})
	}
	return peers, nil
}

func (a *labAuth) subscribeAccountFriends(w http.ResponseWriter, r *http.Request, uid string) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	interval := protoBytes(nil, 3, protoVarint(nil, 1, 50))
	for {
		if _, ok := a.authorizedUser(w, r); !ok {
			return
		}
		peers, err := a.accountFriends(r, uid)
		if err != nil {
			grpcStatus(w, "14", "Account authority unavailable", nil)
			return
		}
		response := append([]byte(nil), interval...)
		for _, p := range peers {
			account := protoBytes(nil, 1, []byte(p.nsa))
			account = protoBytes(account, 2, encodeLocalFriend(uid, p))
			response = protoBytes(response, 1, account)
		}
		if writeGRPCFrame(w, response) != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *labAuth) friendSnapshot(r *http.Request, uid string) ([]localFriendPeer, <-chan struct{}, bool) {
	if a.nextendo != nil {
		peers, err := a.accountFriends(r, uid)
		return peers, nil, err == nil
	}
	if a.friendPair == nil {
		return nil, nil, false
	}
	return a.friendPair.snapshot(uid)
}

// Gamesync uses its own opaque tokens. Bind those issued tokens back to the
// account session which created or joined the room, and recheck during streams.
func (a *labAuth) enforceGamesyncAccounts(next http.Handler, s *ticketStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/nn.npln.gamesync.v1.Gamesync/") {
			next.ServeHTTP(w, r)
			return
		}
		token := splitBearer(r)
		if r.URL.Path == gamesyncIssueTokenPath {
			raw, err := io.ReadAll(io.LimitReader(r.Body, 65542))
			if err != nil || len(raw) > 65541 {
				http.Error(w, "invalid frame", 400)
				return
			}
			r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(raw))
			if len(raw) >= 5 {
				_, token, _ = parseGamesyncIssueToken(raw[5:])
			}
		}
		hash := sha256.Sum256([]byte(token))
		s.mu.Lock()
		issued, ok := s.issuedGamesync[hash]
		if r.URL.Path == gamesyncIssueTokenPath {
			issued, ok = s.issuedMatches[hash]
		}
		s.mu.Unlock()
		allowed := func() bool {
			return ok && time.Now().Before(issued.expires) && a.checkAccountSession(issued.ticket.accountSession, issued.ticket.owner, remotePeerIP(r))
		}
		if !allowed() {
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
			grpcStatus(w, "16", "Account session rejected", nil)
			return
		}
		if r.URL.Path == gamesyncKeepUserSessionPath {
			var end func()
			r.Body, end = a.trackAccountStream(issued.ticket.accountSession, r.Body)
			defer end()
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		done := make(chan struct{})
		defer close(done)
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case <-ticker.C:
					if !allowed() {
						cancel()
						r.Body.Close()
						return
					}
				}
			}
		}()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
