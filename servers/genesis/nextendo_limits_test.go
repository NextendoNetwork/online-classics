package main

import (
	"fmt"
	"testing"
	"time"
)

func TestNextendoRoomCapacityAndClosedRoomReclamation(t *testing.T) {
	s := newTicketStore()
	s.bounded = true
	now := time.Now()
	for i := 0; i < 128; i++ {
		ticket := pendingTicket{sessionName: fmt.Sprintf("gameSessions/fixture-%d", i), owner: "owner", createdAt: now}
		if err := s.publishMatchSession(ticket); err != nil {
			t.Fatal(err)
		}
	}
	if s.publishMatchSession(pendingTicket{sessionName: "gameSessions/overflow", owner: "owner", createdAt: now}) == nil {
		t.Fatal("room capacity exceeded")
	}
	s.sessions["gameSessions/fixture-0"].closed = true
	s.pruneNextendo(now)
	if len(s.sessions) != 127 {
		t.Fatal("closed room not reclaimed")
	}
	if err := s.publishMatchSession(pendingTicket{sessionName: "gameSessions/replacement", owner: "owner", createdAt: now}); err != nil {
		t.Fatal("reclaimed slot not reusable")
	}
}

func TestNextendoCleanupKeepsLiveRoom(t *testing.T) {
	s := newTicketStore()
	s.bounded = true
	host := pendingTicket{sessionName: "gameSessions/active", owner: "host", userName: "userSessions/host", createdAt: time.Now().Add(-time.Hour)}
	if err := s.publishMatchSession(host); err != nil {
		t.Fatal(err)
	}
	room := s.gamesyncRoom(host)
	channel := room.openChannel(gamesyncActorFor(host))
	s.pruneNextendo(time.Now())
	if s.sessions[host.sessionName] == nil || s.rooms[host.sessionName] != room {
		t.Fatal("cleanup evicted live game")
	}
	// Let the existing close path release the channel/host before reclamation.
	room.closeChannel(channel, quiet())
	s.pruneNextendo(time.Now())
	if s.sessions[host.sessionName] != nil || s.rooms[host.sessionName] != nil {
		t.Fatal("abandoned room retained")
	}
}
