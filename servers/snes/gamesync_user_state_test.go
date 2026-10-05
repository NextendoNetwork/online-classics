package main

import (
	"bytes"
	"io"
	"log"
	"testing"
	"time"
)

func TestUserStateSeedIsOptInScopedAndPreservesClientPublication(t *testing.T) {
	for _, tc := range []struct {
		config        string
		enabled, want bool
	}{
		{"LCLA6-2P", false, false}, {"LCLA6-2P", true, true}, {"other", true, false},
	} {
		s := newTicketStore()
		s.genesisUserState = tc.enabled
		ticket := addTestPlayer(s, "access", "room", "player")
		ticket.config = "tenants/current/matchmakingConfigs/" + tc.config
		room := s.gamesyncRoom(ticket)
		path := "docs/__pgn/All/__stu/player"
		doc := room.docs[path]
		if (doc != nil) != tc.want {
			t.Fatalf("scope %+v", tc)
		}
		if !tc.want {
			continue
		}
		if len(doc.fields) != 3 {
			t.Fatal("seed added unverified fields")
		}
		for key, want := range map[string][]byte{"suid": tvStr(ticket.owner), "susid": tvStr("player"), "pl": protoBytes(nil, 8, nil)} {
			got, ok := doc.get(key)
			if !ok || !bytes.Equal(got, want) {
				t.Fatalf("wrong seed type or identity: %s", key)
			}
		}
		actor := gamesyncActorFor(ticket)
		ch := room.openChannel(actor)
		room.installWatch(ch, actor, gamesyncTarget{name: "userSessions/current/targets/9", kind: 2, paths: []string{path}}, log.New(io.Discard, "", 0))
		frames, _ := ch.q.next()
		if len(frames) != 3 {
			t.Fatal("seed not delivered between UPDATED and LISTED")
		}
		snapshot := decodeTestMessage(t, decodeTestMessage(t, frames[1])[2])
		if !bytes.Equal(snapshot[2], []byte{1}) {
			t.Fatal("seed snapshot must be EXIST")
		}
		live := protoBytes(nil, 8, []byte("test-client-connection-data"))
		_, _, err := room.applyWrites(actor, []gsWrite{{kind: 1, path: path, mask: []string{"pl"}, fields: []gsField{{"pl", live}}}}, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		s.gamesyncRoom(ticket)
		got, _ := room.docs[path].get("pl")
		if !bytes.Equal(got, live) {
			t.Fatal("subsequent registration overwrote client pl")
		}
		other := addTestPlayer(s, "other-access", "other-room", "player")
		other.config = ticket.config
		otherRoom := s.gamesyncRoom(other)
		otherPl, _ := otherRoom.docs[path].get("pl")
		if !bytes.Equal(otherPl, protoBytes(nil, 8, nil)) {
			t.Fatal("client publication leaked to another room")
		}
		room.closeChannel(ch, log.New(io.Discard, "", 0))
	}
}
