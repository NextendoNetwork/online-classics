package main

import (
	"bytes"
	"log"
	"sort"
)

// Snapshot after a write, bounded per room. Types, sizes and comparisons
// only: never values, opaque names, IDs or hashes.
// The snapshot may include another concurrent write already committed.
func (room *gamesyncRoom) logParticipantStructure(actor gamesyncActor, logger *log.Logger) {
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.diagnosticSnapshots >= 32 {
		return
	}
	room.diagnosticSnapshots++
	logger.Printf("PIA structure: snapshot=%d participants=%d", room.diagnosticSnapshots, len(room.participants))
	seed := room.docs["docs/__us/"+actor.usid]
	presence := room.docs["docs/__pgn/All/__pus/"+actor.usid]
	for _, key := range []string{"ussid", "ucsid", "upcsid"} {
		var seeded, published []byte
		var seedOK, presenceOK bool
		if seed != nil {
			seeded, seedOK = seed.get(key)
		}
		if presence != nil {
			published, presenceOK = presence.get(key)
		}
		comparable, equal := 0, 0
		for other := range room.participants {
			if other == actor.usid || !presenceOK {
				continue
			}
			if doc := room.docs["docs/__pgn/All/__pus/"+other]; doc != nil {
				if value, ok := doc.get(key); ok {
					comparable++
					if bytes.Equal(value, published) {
						equal++
					}
				}
			}
		}
		logger.Printf("PIA sequence: field=%s session_present=%t presence_present=%t matches_session=%t other_comparable=%d other_equal=%d", key, seedOK, presenceOK, seedOK && presenceOK && bytes.Equal(seeded, published), comparable, equal)
	}
	var paths []string
	for path := range room.docs {
		p := classifyGamesyncPath(path)
		if p.id == actor.usid && (p.kind == gsPathUserSession || p.kind == gsPathPresence || p.kind == gsPathUserState || p.kind == gsPathGameDocument) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		doc := room.docs[path]
		p := classifyGamesyncPath(path)
		logger.Printf("PIA document: path_schema=%q fields=%d", documentPathShape(path), len(doc.fields))
		for i, f := range doc.fields {
			kind, err := gsValueKind(f.value)
			comparable, equal := 0, 0
			if p.kind == gsPathGameDocument {
				for _, otherPath := range pathsForOtherGameDocuments(room, path, actor.usid) {
					if value, ok := room.docs[otherPath].get(f.key); ok {
						comparable++
						if bytes.Equal(value, f.value) {
							equal++
						}
					}
				}
			}
			logger.Printf("PIA field: index=%d type=%d valid=%t encoded_bytes=%d other_comparable=%d other_equal=%d", i, kind, err == nil, len(f.value), comparable, equal)
		}
	}
}

// Caller holds room.mu. Compare only same-collection documents of current peers.
func pathsForOtherGameDocuments(room *gamesyncRoom, path, usid string) []string {
	collection := path[:len(path)-len(usid)]
	var paths []string
	for otherPath := range room.docs {
		p := classifyGamesyncPath(otherPath)
		if p.kind == gsPathGameDocument && p.id != usid && room.participants[p.id] && otherPath[:len(otherPath)-len(p.id)] == collection {
			paths = append(paths, otherPath)
		}
	}
	return paths
}
