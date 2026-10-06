package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestPIADiagnosticComparesPublishedSequencesWithoutPrivateData(t *testing.T) {
	room := newGamesyncRoom()
	host := pendingTicket{owner: "private-owner-host", userName: "userSessions/private-host", sessionName: "gameSessions/private-room", createdAt: time.Now()}
	guest := host
	guest.owner, guest.userName = "private-owner-guest", "userSessions/private-guest"
	room.register(host)
	room.register(guest)
	for _, id := range []string{"private-host", "private-guest"} {
		// Deliberate discrepancy: both publish 1, but guest's seeded rank is 2.
		room.docs["docs/__pgn/All/__pus/"+id] = &gsDoc{fields: []gsField{{"ussid", tvInt(1)}, {"ucsid", tvInt(1)}, {"upcsid", tvInt(1)}}}
		room.docs["docs/private-collection/"+id] = &gsDoc{fields: []gsField{{"private-field", protoBytes(nil, 8, []byte("private-secret-endpoint"))}}}
	}
	var out bytes.Buffer
	logger := log.New(&out, "", 0)
	room.logParticipantStructure(gamesyncActorFor(guest), logger)
	text := out.String()
	if !strings.Contains(text, "matches_session=false other_comparable=1 other_equal=1") || !strings.Contains(text, "type=8 valid=true") {
		t.Fatal("missing sequence discrepancy or opaque-field structure")
	}
	if strings.Contains(text, "private-") {
		t.Fatal("private identifiers, keys or values exposed")
	}
	for i := 0; i < 40; i++ {
		room.logParticipantStructure(gamesyncActorFor(guest), logger)
	}
	if strings.Count(out.String(), "PIA structure:") != 32 {
		t.Fatal("diagnostic output is not bounded")
	}
}
