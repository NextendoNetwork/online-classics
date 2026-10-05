package main

import (
	"bytes"
	"io"
	"log"
	"testing"
	"time"
)

func signalingTestRoom() (*gamesyncRoom, gamesyncActor, gamesyncActor) {
	room := newGamesyncRoom()
	host := gamesyncActor{usid: "host", owner: "owner-host", ready: true}
	guest := gamesyncActor{usid: "guest", owner: "owner-guest", ready: true}
	for _, actor := range []gamesyncActor{host, guest} {
		room.register(pendingTicket{userName: "userSessions/" + actor.usid, owner: actor.owner, createdAt: time.Now()})
	}
	return room, host, guest
}

func signalingTestFields(actor gamesyncActor) []gsField {
	return []gsField{fld("susid", tvStr(actor.usid)), fld("suid", tvStr(actor.owner)),
		fld("sussid", tvInt(2)), fld("suscid", tvInt(1)), fld("pl", protoBytes(nil, 8, []byte{11, 22, 0}))}
}

func TestSignalOpaqueKeyReachesPeerAndDeferredCleanup(t *testing.T) {
	room, host, guest := signalingTestRoom()
	logger := log.New(io.Discard, "", 0)
	reader := room.openChannel(host)
	writer := room.openChannel(guest)
	defer room.closeChannel(reader, logger)
	room.installWatch(reader, host, gamesyncTarget{name: "userSessions/current/targets/1", kind: 3, paths: []string{"docs/__pgn/All/__stu"}}, logger)
	reader.q.next() // UPDATED + LISTED
	path := "docs/__pgn/All/__stu/message-to-host"
	fields := signalingTestFields(guest)
	request := testWriteRequest([][]byte{testOpUpdate(path, []string{"*"}, fields...)}, testDeferUpdate("userSessions/current/deferments/cleanup-signal", testOpDelete(path)))
	writes, defs, err := gsParseWriteRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = room.applyWrites(guest, writes, defs, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	frames, _ := reader.q.next()
	if len(frames) != 1 || !bytes.Contains(frames[0], []byte(path)) || !bytes.Contains(frames[0], fields[4].value) {
		t.Fatal("peer did not receive the unchanged signaling document")
	}
	if err := room.authorizeRead(host, classifyGamesyncPath(path)); err != nil {
		t.Fatal(err)
	}
	_, _, err = room.applyWrites(guest, []gsWrite{{kind: 1, path: path, mask: []string{"`pl`"}, fields: []gsField{fld("pl", protoBytes(nil, 8, []byte{33, 1}))}}}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	reader.q.next()
	room.closeChannel(writer, logger)
	if room.docs[path] != nil {
		t.Fatal("sender's deferred cleanup did not delete the signal")
	}
	frames, _ = reader.q.next()
	if len(frames) == 0 {
		t.Fatal("peer did not receive cleanup")
	}
}

func TestSignalRejectsSpoofingForeignOverwriteAndAtomicBatch(t *testing.T) {
	room, host, guest := signalingTestRoom()
	path := "docs/__pgn/All/__stu/message"
	valid := gsWrite{kind: 1, path: path, mask: []string{"*"}, fields: signalingTestFields(guest)}
	for _, field := range []string{"susid", "suid"} {
		bad := valid
		bad.fields = append([]gsField(nil), valid.fields...)
		for i := range bad.fields {
			if bad.fields[i].key == field {
				bad.fields[i].value = tvStr("other-player")
			}
		}
		_, _, err := room.applyWrites(guest, []gsWrite{valid, bad}, nil, time.Now())
		if err == nil || err.code != "7" || room.docs[path] != nil {
			t.Fatal("spoofed batch committed")
		}
	}
	if _, _, err := room.applyWrites(guest, []gsWrite{valid}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, op := range []gsWrite{
		{kind: 4, path: path},
		{kind: 1, path: path, mask: []string{"*"}, fields: signalingTestFields(host)},
		{kind: 1, path: "docs/__pgn/All/__pus/guest", mask: []string{"*"}, fields: []gsField{fld("uid", tvStr(host.owner))}},
	} {
		if _, _, err := room.applyWrites(host, []gsWrite{op}, nil, time.Now()); err == nil || err.code != "7" {
			t.Fatal("foreign document modified")
		}
	}
	outsider := gamesyncActor{usid: "outsider", owner: "owner-outsider", ready: true}
	op := valid
	op.path += "-outside"
	op.fields = signalingTestFields(outsider)
	if _, _, err := room.applyWrites(outsider, []gsWrite{op}, nil, time.Now()); err == nil {
		t.Fatal("outsider published signal")
	}
	if err := room.authorizeRead(outsider, classifyGamesyncPath(path)); err == nil {
		t.Fatal("outsider read signal")
	}
}

func TestSignalReconnectReusesKeyOnlyAfterLastSenderChannelCloses(t *testing.T) {
	room, host, guest := signalingTestRoom()
	logger := log.New(io.Discard, "", 0)
	first := room.openChannel(guest)
	second := room.openChannel(guest)
	path := "docs/__pgn/All/__stu/reused-signal-key"
	peerPath := "docs/__pgn/All/__stu/peer-signal-key"
	for p, actor := range map[string]gamesyncActor{path: guest, peerPath: host} {
		if _, _, err := room.applyWrites(actor, []gsWrite{{kind: 1, path: p, mask: []string{"*"}, fields: signalingTestFields(actor)}}, nil, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	reconnected := gamesyncActor{usid: "guest-new-session", owner: guest.owner, ready: true}
	room.register(pendingTicket{userName: "userSessions/" + reconnected.usid, owner: reconnected.owner, createdAt: time.Now()})
	op := gsWrite{kind: 1, path: path, mask: []string{"*"}, fields: signalingTestFields(reconnected)}
	room.closeChannel(first, logger)
	if room.docs[path] == nil {
		t.Fatal("signal removed while another sender channel remained open")
	}
	if _, _, err := room.applyWrites(reconnected, []gsWrite{op}, nil, time.Now()); err == nil {
		t.Fatal("live sender's key was taken over")
	}
	room.closeChannel(second, logger)
	if room.docs[path] != nil || room.docs[peerPath] == nil {
		t.Fatal("last-channel cleanup did not isolate sender ownership")
	}
	if _, _, err := room.applyWrites(reconnected, []gsWrite{op}, nil, time.Now()); err != nil {
		t.Fatal("reconnected sender could not reuse cleaned key:", err)
	}
}
