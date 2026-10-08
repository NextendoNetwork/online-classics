package main

import "time"

// Caller holds the store lock. Active Gamesync channels survive cleanup; closed
// or abandoned rooms without a channel are reclaimed with their credentials.
func (s *ticketStore) pruneNextendoLocked(now time.Time) {
	if !s.bounded {
		return
	}
	for key, v := range s.tickets {
		if now.Sub(v.createdAt) > time.Hour {
			delete(s.tickets, key)
		}
	}
	for key, v := range s.issuedMatches {
		if !v.expires.After(now) {
			delete(s.issuedMatches, key)
		}
	}
	for key, v := range s.issuedGamesync {
		if !v.expires.After(now) {
			delete(s.issuedGamesync, key)
		}
	}
	for name, v := range s.sessions {
		if !v.closed && now.Sub(v.host.createdAt) < 10*time.Minute {
			continue
		}
		active := false
		if room := s.rooms[name]; room != nil {
			room.mu.Lock()
			active = len(room.channels) > 0
			room.mu.Unlock()
		}
		if active {
			continue
		}
		delete(s.sessions, name)
		delete(s.rooms, name)
		for key, issued := range s.issuedMatches {
			if issued.ticket.sessionName == name {
				delete(s.issuedMatches, key)
			}
		}
		for key, issued := range s.issuedGamesync {
			if issued.ticket.sessionName == name {
				delete(s.issuedGamesync, key)
			}
		}
	}
}

func (s *ticketStore) pruneNextendo(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneNextendoLocked(now)
}

func (s *nextendoSessions) cleanup(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, v := range s.sessions {
		if !v.expires.After(now) {
			delete(s.sessions, id)
		}
	}
	for hash, id := range s.refresh {
		if _, exists := s.sessions[id]; !exists {
			delete(s.refresh, hash)
		}
	}
}
