package main

import (
	"bytes"
	"log"
)

// Genesis 3.1.1 main 0x2f3380 builds suid/susid/sussid/suscid/pl
// from the sender object. Its independent x1 argument becomes the __stu
// document key at 0x2f34e8; requiring key == sender USID rejects signaling.
// Keep ownership on the server, separately from the client fields.
// Caller holds room.mu; cur can be the tentative document of this batch.
func (room *gamesyncRoom) authorizeSignalWrite(actor gamesyncActor, wr gsWrite, cur *gsDoc) *gsError {
	p := classifyGamesyncPath(wr.path)
	if p.kind != gsPathUserState || p.id == actor.usid {
		// A signal already owned by a peer must not become writable merely
		// because its opaque key happens to equal this actor's session ID.
		if p.kind == gsPathUserState && cur != nil && cur.signalingOwner != "" && cur.signalingOwner != actor.usid {
			return gsFail("7", "Signaling from another sender")
		}
		return gsAuthorizeWrite(actor, p)
	}
	if actor.usid == "" {
		return gsFail("16", "Participant has no identity")
	}
	if !room.participants[actor.usid] {
		return gsFail("7", "Signaling sender outside the room")
	}
	if cur != nil && cur.signalingOwner != actor.usid {
		return gsFail("7", "Signaling from another sender")
	}
	switch wr.kind {
	case 1:
		if actor.owner == "" {
			return gsFail("16", "Signaling sender has no user")
		}
	case 4: // includes authenticated sender's deferred cleanup
	default:
		return gsFail("12", "Unobserved signaling operation")
	}
	return nil
}

func (room *gamesyncRoom) validateSignalDocument(actor gamesyncActor, doc *gsDoc) *gsError {
	for key, expected := range map[string][]byte{
		"susid": protoBytes(nil, 7, []byte(actor.usid)),
		"suid":  protoBytes(nil, 7, []byte(actor.owner)),
	} {
		value, exists := doc.get(key)
		if !exists || !bytes.Equal(value, expected) {
			return gsFail("7", "Incorrect signaling sender identity: "+key)
		}
	}
	for _, key := range []string{"sussid", "suscid"} {
		value, exists := doc.get(key)
		kind, err := gsValueKind(value)
		if !exists || err != nil || kind != 3 {
			return gsFail("3", "Invalid signaling sequence: "+key)
		}
	}
	// Both observed SDK forms carry a typed payload: pl (bytes) or mp (map).
	payload := false
	for key, expectedKind := range map[string]uint64{"pl": 8, "mp": 10} {
		if value, exists := doc.get(key); exists {
			kind, err := gsValueKind(value)
			if err != nil || kind != expectedKind {
				return gsFail("3", "Invalid signaling payload: "+key)
			}
			payload = true
		}
	}
	if !payload {
		return gsFail("3", "Signaling without payload")
	}
	return nil
}

// Log comparisons and sizes only, including requests rejected by validation.
func (room *gamesyncRoom) logSignalStructure(actor gamesyncActor, writes []gsWrite, logger *log.Logger) {
	room.mu.Lock()
	defer room.mu.Unlock()
	for _, wr := range writes {
		if classifyGamesyncPath(wr.path).kind != gsPathUserState {
			continue
		}
		usid, _ := gsLookup(wr.fields, "susid")
		uid, _ := gsLookup(wr.fields, "suid")
		payload, hasPayload := gsLookup(wr.fields, "pl")
		watchers := 0
		for ch := range room.channels {
			if ch.usid == actor.usid {
				continue
			}
			for _, watch := range ch.watches {
				if watch.matches(wr.path) {
					watchers++
					break
				}
			}
		}
		logger.Printf("PIA signaling: operation=%d sender_registered=%t susid_matches_sender=%t suid_matches_user=%t pl_present=%t pl_encoded_bytes=%d receiver_channels=%d", wr.kind, room.participants[actor.usid], bytes.Equal(usid, protoBytes(nil, 7, []byte(actor.usid))), bytes.Equal(uid, protoBytes(nil, 7, []byte(actor.owner))), hasPayload, len(payload), watchers)
	}
}
