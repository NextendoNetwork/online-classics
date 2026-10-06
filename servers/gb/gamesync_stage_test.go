package main

import (
	"bytes"
	"crypto/sha256"
	"testing"
)

func TestStageExperimentUsesHostAndSharesWatchSnapshot(t *testing.T) {
	s := newTicketStore()
	s.genesisStage = true
	host := addTestPlayer(s, "host-access", "stage-room", "host")
	host.config = "tenants/current/matchmakingConfigs/LCLA6-2P"
	host.request = requestedGameSession{capacity: 4, password: "room-password", properties: decodeTestMessage(t, testDocBytes("props", fld("consoleName", tvStr("MD"))))[2]}
	if err := s.publishMatchSession(host); err != nil {
		t.Fatal(err)
	}
	guest := addTestPlayer(s, "guest-access", "stage-room", "guest")
	// Guest-first access must not publish guest settings instead of the host's.
	status, fields := callGet(t, s, "guest-access", "docs/__stg/All")
	if status != "0" || !bytes.Equal(fields["gsid"], tvStr("stage-room")) || !bytes.Equal(fields["mcn"], tvStr("LCLA6-2P")) || intOf(t, fields["maxu"]) != 4 {
		t.Fatal("stage does not describe the host room")
	}
	settingsMap, present := decodeTestMessage(t, fields["rs"])[10]
	if !present {
		t.Fatal("rs must be a map, not the fixed-session secret")
	}
	_, settings, err := gsParseDocument(protoBytes(protoBytes(nil, 1, []byte("settings")), 2, settingsMap))
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := gsLookup(settings, "pw")
	ip, _ := gsLookup(settings, "ip")
	props, _ := gsLookup(settings, "prp")
	if !bytes.Equal(pw, tvStr(host.request.password)) || !bytes.Equal(ip, protoVarint(nil, 2, 0)) || !bytes.Equal(decodeTestMessage(t, props)[10], host.request.properties) {
		t.Fatal("nested settings lost host values or types")
	}
	issued := s.issuedGamesync[sha256.Sum256([]byte("host-access"))]
	issued.ticket = host
	s.issuedGamesync[sha256.Sum256([]byte("host-access"))] = issued
	room := s.gamesyncRoom(host)
	actor := gamesyncActorFor(guest)
	before, readErr := room.readDocument(actor, "docs/__stg/All")
	if readErr != nil {
		t.Fatal(readErr)
	}
	ch := room.openChannel(actor)
	defer room.closeChannel(ch, quiet())
	room.installWatch(ch, actor, gamesyncTarget{name: "userSessions/current/targets/stage", kind: 2, paths: []string{"docs/__stg/All"}}, quiet())
	frames, _ := ch.q.next()
	if len(frames) != 3 || !bytes.Equal(decodeTestMessage(t, decodeTestMessage(t, frames[1])[2])[3], before) {
		t.Fatal("stage watch and read disagree")
	}
	for _, access := range []string{"host-access", "guest-access"} {
		if status, _ := callWrite(t, s, access, testWriteRequest([][]byte{testOpDelete("docs/__stg/All")}), quiet()); status != "12" {
			t.Fatal("client changed server stage")
		}
	}
	after, readErr := room.readDocument(actor, "docs/__stg/All")
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatal("stage changed after another registration or rejected write")
	}
	// Other rooms retain independent settings, and state-user remains absent.
	other := host
	other.sessionName += "-other"
	other.request.password = "other-password"
	otherRoom := s.gamesyncRoom(other)
	otherSettings, _ := otherRoom.docs["docs/__stg/All"].get("rs")
	if otherRoom == room || bytes.Equal(otherSettings, fields["rs"]) {
		t.Fatal("stage shared between rooms")
	}
	if _, err := room.readDocument(actor, "docs/__pgn/All/__stu/guest"); err == nil {
		t.Fatal("unconfirmed state-user was fabricated")
	}
}

func TestStageExperimentDefaultOffAndGenesisOnly(t *testing.T) {
	for _, tc := range []struct {
		enabled bool
		config  string
	}{{false, "LCLA6-2P"}, {true, "LCLA6"}, {true, "other-game"}} {
		s := newTicketStore()
		s.genesisStage = tc.enabled
		ticket := addTestPlayer(s, "access", "room", "host")
		ticket.config = "tenants/current/matchmakingConfigs/" + tc.config
		if s.gamesyncRoom(ticket).docs["docs/__stg/All"] != nil {
			t.Fatal("stage enabled outside experiment scope")
		}
	}
}
