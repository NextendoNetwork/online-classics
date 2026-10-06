package main

import (
	"bytes"
	"crypto/sha256"
	"testing"
	"time"
)

func TestFixedSessionStableAndBoundToRoom(t *testing.T) {
	s := newTicketStore()
	first := addTestPlayer(s, "a", "room-a", "a")
	first.config = "tenants/current/matchmakingConfigs/LCLA6-2P"
	issued := s.issuedGamesync[sha256.Sum256([]byte("a"))]
	issued.ticket = first
	s.issuedGamesync[sha256.Sum256([]byte("a"))] = issued
	addTestPlayer(s, "b", "room-b", "b")
	status, a := callGet(t, s, "a", "docs/__gs/f")
	if status != "0" {
		t.Fatal(status)
	}
	_, b := callGet(t, s, "b", "docs/__gs/f")
	_, again := callGet(t, s, "a", "docs/__gs/f")
	if !bytes.Equal(a["gsid"], tvStr("room-a")) || !bytes.Equal(a["mcn"], tvStr(resourceLeaf(first.config))) || !bytes.Equal(a["tid"], tvStr(labTenant)) || intOf(t, a["maxu"]) != 2 {
		t.Fatal("Fields do not match the ticket")
	}
	secret := decodeTestMessage(t, a["rs"])[8]
	if len(secret) != 32 || !bytes.Equal(a["rs"], again["rs"]) || bytes.Equal(a["rs"], b["rs"]) {
		t.Fatal("Secret unstable or shared between rooms")
	}
	if got, _ := callWrite(t, s, "a", testWriteRequest([][]byte{testOpDelete("docs/__gs/f")}), quiet()); got != "12" {
		t.Fatal("Client deleted a server document")
	}
	room := s.gamesyncRoom(first)
	actor := gamesyncActorFor(first)
	channel := room.openChannel(actor)
	defer room.closeChannel(channel, quiet())
	room.installWatch(channel, actor, gamesyncTarget{name: "userSessions/current/targets/fixed", kind: 2, paths: []string{"docs/__gs/f"}}, quiet())
	frames, ok := channel.q.next()
	if !ok || len(frames) != 3 {
		t.Fatal("Watch does not publish the fixed document")
	}
	change := decodeTestMessage(t, decodeTestMessage(t, frames[1])[2])
	get, err := room.readDocument(actor, "docs/__gs/f")
	if err != nil || !bytes.Equal(change[3], get) {
		t.Fatal("Watch and GetDocument differ")
	}
}

func TestOpaqueReservedClientDocumentPermissions(t *testing.T) {
	s := newTicketStore()
	ticket := addTestPlayer(s, "a", "room", "a")
	addTestPlayer(s, "b", "room", "b")
	p := "docs/__clientOpaque/a"
	if got, _ := callWrite(t, s, "a", testWriteRequest([][]byte{testOpUpdate(p, []string{"*"}, fld("data", tvStr("opaque")))}), quiet()); got != "0" {
		t.Fatal(got)
	}
	if got, _ := callGet(t, s, "a", p); got != "0" {
		t.Fatal(got)
	}
	if got, _ := callWrite(t, s, "b", testWriteRequest([][]byte{testOpDelete(p)}), quiet()); got != "7" {
		t.Fatal("Unauthorized write permitted")
	}
	room := s.gamesyncRoom(ticket)
	for _, path := range []string{"docs/__gs/unknown", "docs/__us/unknown", "docs/__pgn/unknown", "docs/__stg/unknown"} {
		_, _, err := room.applyWrites(gamesyncActorFor(ticket), []gsWrite{{kind: 4, path: path}}, nil, time.Now())
		if err == nil {
			t.Fatal("Reserved root modified", path)
		}
	}
}
