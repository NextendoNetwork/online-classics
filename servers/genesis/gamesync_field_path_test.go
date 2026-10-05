package main

import (
	"bytes"
	"testing"
	"time"
)

// Reproduces Genesis main 0x2f6854..0x2f6900: load the room counter,
// maximum(20000), increment(1), store it. The original bug produced a sixth
// field named `upcsid`, leaving the actual upcsid at zero for both players.
func TestGenesisQuotedCounterUpdatesPresenceAndAllocatesDistinctIDs(t *testing.T) {
	room := newGamesyncRoom()
	for i, id := range []string{"host", "guest"} {
		actor := gamesyncActor{usid: id}
		room.participants[id] = true
		path := "docs/__pgn/All/__pus/" + id
		request := protoBytes(nil, 1, testOpUpdate(path, []string{"*"},
			fld("uid", tvStr(id)), fld("ussid", tvInt(int64(i+1))),
			fld("ucsid", tvInt(int64(i+1))), fld("pgn", tvStr("All")), fld("upcsid", tvInt(0))))
		request = protoBytes(request, 1, testOpTransform(path,
			testFT("`upcsid`", 6, nil, "__upcsidn"),
			testFT("`upcsid`", 4, tvInt(20000), ""),
			testFT("`upcsid`", 3, tvInt(1), ""),
			testFT("`upcsid`", 7, nil, "__upcsidn")))
		writes, defs, err := gsParseWriteRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = room.applyWrites(actor, writes, defs, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		doc := room.docs[path]
		want := tvInt(int64(20001 + i))
		got, _ := doc.get("upcsid")
		if !bytes.Equal(got, want) || len(doc.fields) != 5 || !bytes.Equal(room.globals["__upcsidn"], want) {
			t.Fatalf("participant %s: counter not applied to the canonical field (fields=%d)", id, len(doc.fields))
		}
		if _, extra := doc.get("`upcsid`"); extra {
			t.Fatal("quoted duplicate field")
		}
	}
}

func TestGenesisQuotedMaskAndUnsupportedPathsRemainAtomic(t *testing.T) {
	room := newGamesyncRoom()
	actor := gamesyncActor{usid: "host"}
	room.participants[actor.usid] = true
	path := "docs/__pgn/All/__pus/host"
	room.docs[path] = &gsDoc{fields: []gsField{fld("upcsid", tvInt(1)), fld("uid", tvStr("host"))}}
	_, _, err := room.applyWrites(actor, []gsWrite{{kind: 1, path: path, mask: []string{"`upcsid`"}, fields: []gsField{fld("upcsid", tvInt(2))}}}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	value, _ := room.docs[path].get("upcsid")
	if !bytes.Equal(value, tvInt(2)) || len(room.docs[path].fields) != 2 || !gsInMask([]string{"`upcsid`"}, "upcsid") {
		t.Fatal("quoted mask not applied")
	}
	for _, bad := range []string{"`upcsid", "upcsid`", "`a`.`b`", "`a\\`b`", "a.b", "``"} {
		_, _, err := room.applyWrites(actor, []gsWrite{{kind: 3, path: path, transforms: []gsTransform{{path: "`upcsid`", kind: 3, operand: tvInt(1)}, {path: bad, kind: 3, operand: tvInt(1)}}}}, nil, time.Now())
		value, _ := room.docs[path].get("upcsid")
		if err == nil || !bytes.Equal(value, tvInt(2)) {
			t.Fatalf("invalid path %q was committed", bad)
		}
	}
}
