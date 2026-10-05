package main

import (
	"bytes"
	"testing"
	"time"
)

func TestParticipantRanksStableUniqueAndRoomScoped(t *testing.T) {
	room := newGamesyncRoom()
	makeTicket := func(id string) pendingTicket {
		return pendingTicket{owner: "owner-" + id, sessionName: "gameSessions/room", userName: "userSessions/" + id, createdAt: time.Now()}
	}
	read := func(ticket pendingTicket, rank int64) []byte {
		t.Helper()
		path := "docs/__us/" + resourceLeaf(ticket.userName)
		doc, err := room.readDocument(gamesyncActorFor(ticket), path)
		if err != nil {
			t.Fatal(err)
		}
		_, fields, err := gsParseDocument(doc)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"ussid", "ucsid", "upcsid"} {
			value, ok := gsLookup(fields, key)
			if !ok || !bytes.Equal(value, tvInt(rank)) {
				t.Fatalf("%s does not contain rank %d", key, rank)
			}
		}
		return doc
	}
	host, guest, third := makeTicket("host"), makeTicket("guest"), makeTicket("third")
	for _, ticket := range []pendingTicket{host, guest, third} {
		room.register(ticket)
	}
	read(host, 1)
	read(guest, 2)
	read(third, 3)
	room.register(guest)
	before := read(guest, 2)
	ch := room.openChannel(gamesyncActorFor(guest))
	room.installWatch(ch, gamesyncActorFor(guest), gamesyncTarget{name: "userSessions/current/targets/1", kind: 2, paths: []string{"docs/__us/guest"}}, quiet())
	frames, _ := ch.q.next()
	if len(frames) != 3 || !bytes.Equal(decodeTestMessage(t, decodeTestMessage(t, frames[1])[2])[3], before) {
		t.Fatal("watch and read differ")
	}
	if _, err := room.readDocument(gamesyncActorFor(host), "docs/__us/outside-room"); err == nil {
		t.Fatal("session outside room readable")
	}
	room.closeChannel(ch, quiet())
	// A new session after departure must not reuse the departed rank.
	reconnected := makeTicket("new-guest-session")
	room.register(reconnected)
	read(reconnected, 4)
	room.register(host)
	read(host, 1)
	other := newGamesyncRoom()
	other.register(makeTicket("other-host"))
	if other.lastParticipantRank != 1 {
		t.Fatal("rank shared between rooms")
	}
}
