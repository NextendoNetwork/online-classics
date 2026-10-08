package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

const gamesyncIssueTokenPath = "/nn.npln.gamesync.v1.Gamesync/IssueToken"

type issuedMatch struct {
	ticket  pendingTicket
	expires time.Time
}

func (s *ticketStore) rememberMatchToken(ticket pendingTicket, token string, now time.Time) *gsError {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, issued := range s.issuedMatches {
		if !now.Before(issued.expires) {
			delete(s.issuedMatches, key)
		}
	}
	s.pruneNextendoLocked(now)
	if s.bounded && len(s.issuedMatches) >= 4096 {
		return gsFail("8", "Match credential capacity reached")
	}
	// Conservative limit for both profiles: the gss token lasts one hour.
	s.issuedMatches[sha256.Sum256([]byte(token))] = issuedMatch{ticket: ticket, expires: now.Add(time.Hour)}
	return nil
}

func parseGamesyncIssueToken(payload []byte) (string, string, error) {
	var session, token string
	seen := map[uint64]bool{}
	err := visitProto(payload, func(field, wire, _ uint64, value []byte) error {
		if field != 1 && field != 2 {
			return nil
		}
		if wire != 2 || seen[field] {
			return errors.New("Invalid IssueToken field")
		}
		seen[field] = true
		if field == 1 {
			session = string(value)
		} else {
			token = string(value)
		}
		return nil
	})
	if err == nil && (session == "" || token == "") {
		err = errors.New("Incomplete IssueToken")
	}
	return session, token, err
}

// Gamesync may identify a participant by UUID within the session channel.
// Resolve it ONLY against the ticket bound to the token, avoiding a global
// session lookup or acceptance of a participant from another room.
func matchingGamesyncSession(requested, expected string) (string, bool) {
	if requested == expected {
		return "full", true
	}
	alias := strings.Replace(expected, "tenants/"+labTenant+"/", "tenants/current/", 1)
	if requested == alias {
		return "tenant-current", true
	}
	relative := strings.TrimPrefix(expected, "tenants/"+labTenant+"/")
	if relative != expected && requested == relative {
		return "gameSessions-userSessions", true
	}
	leaf := expected[strings.LastIndexByte(expected, '/')+1:]
	if leaf != "" && requested == leaf {
		return "participant-id", true
	}
	if leaf != "" && requested == "userSessions/"+leaf {
		return "userSessions-id", true
	}
	return "unrecognized", false
}

// Gamesync does not require npln-tenant-id: the observed client omits it.
// Authorization comes from the exact token issued in SUCCEEDED for this session.
// External tokens are never accepted and token bodies are never logged.
func (s *ticketStore) issueGamesyncToken(a *labAuth, logger *log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		if tenant := r.Header.Get("npln-tenant-id"); tenant != "" && tenant != labTenant {
			grpcStatus(w, "3", "Incorrect tenant", nil)
			return
		}
		payload, err := grpcRequestPayload(r)
		if err != nil {
			grpcStatus(w, "3", "Invalid frame", nil)
			return
		}
		session, matchToken, err := parseGamesyncIssueToken(payload)
		if err != nil {
			grpcStatus(w, "3", "Invalid IssueToken", nil)
			return
		}
		s.mu.Lock()
		issued, exists := s.issuedMatches[sha256.Sum256([]byte(matchToken))]
		s.mu.Unlock()
		form, matches := matchingGamesyncSession(session, issued.ticket.userName)
		logger.Printf("Gamesync IssueToken reference: format=%s length=%d segments=%d matches=%t", form, len(session), len(strings.Split(session, "/")), exists && matches)
		if !exists || !time.Now().Before(issued.expires) || !matches {
			logger.Printf("Gamesync IssueToken: local_token_recognized=%t session_matches=%t", exists, exists && matches)
			grpcStatus(w, "16", "Invalid local token or session", nil)
			return
		}
		access, err := a.nplnSessionToken(issued.ticket, time.Now())
		if err != nil {
			grpcStatus(w, "13", "Could not issue Gamesync token", nil)
			return
		}
		s.mu.Lock()
		// Departure may have revoked the match while access was being signed.
		current, stillIssued := s.issuedMatches[sha256.Sum256([]byte(matchToken))]
		if !stillIssued || !time.Now().Before(current.expires) {
			s.mu.Unlock()
			grpcStatus(w, "16", "Local session closed", nil)
			return
		}
		for key, previous := range s.issuedGamesync {
			if !time.Now().Before(previous.expires) {
				delete(s.issuedGamesync, key)
			}
		}
		s.pruneNextendoLocked(time.Now())
		if s.bounded && len(s.issuedGamesync) >= 4096 {
			s.mu.Unlock()
			grpcStatus(w, "8", "Gamesync credential capacity reached", nil)
			return
		}
		s.issuedGamesync[sha256.Sum256([]byte(access))] = issuedMatch{ticket: issued.ticket, expires: time.Now().Add(8 * time.Hour)}
		s.mu.Unlock()
		var refresh [24]byte
		if _, err := rand.Read(refresh[:]); err != nil {
			grpcStatus(w, "13", "Could not issue refresh token", nil)
			return
		}
		// IssueTokenResponse.token = Token{user_session,access_token,refresh_token,ttl}.
		// Preserve the representation used by the client on this channel; JWT claims
		// retain the ticket's complete, verifiable resource name.
		token := protoBytes(nil, 1, []byte(session))
		token = protoBytes(token, 2, []byte(access))
		token = protoBytes(token, 3, []byte(hex.EncodeToString(refresh[:])))
		token = protoBytes(token, 4, protoVarint(nil, 1, 8*3600))
		logger.Print("Gamesync IssueToken: local_token_recognized=true session_matches=true ttl=28800")
		grpcStatus(w, "0", "", protoBytes(nil, 1, token))
	}
}
