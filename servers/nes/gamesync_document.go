package main

import (
	"crypto/sha256"
	"log"
	"net/http"
	"strings"
	"time"
)

const gamesyncGetDocumentPath = "/nn.npln.gamesync.v1.Gamesync/GetDocument"

// Point reads use the same per-room store as KeepUserSession, avoiding
// contradictions with earlier notifications or committed writes.
func (s *ticketStore) getGamesyncDocument(logger *log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		if tenant := r.Header.Get("npln-tenant-id"); tenant != "" && tenant != labTenant {
			grpcStatus(w, "3", "Incorrect tenant", nil)
			return
		}
		parts := strings.Fields(r.Header.Get("authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			grpcStatus(w, "16", "Missing local Gamesync token", nil)
			return
		}
		s.mu.Lock()
		issued, exists := s.issuedGamesync[sha256.Sum256([]byte(parts[1]))]
		s.mu.Unlock()
		if !exists || !time.Now().Before(issued.expires) {
			grpcStatus(w, "16", "Invalid local Gamesync token", nil)
			return
		}
		payload, err := grpcRequestPayload(r)
		if err != nil {
			grpcStatus(w, "3", "Invalid frame", nil)
			return
		}
		name, err := protoStringField1(payload)
		if err != nil {
			grpcStatus(w, "3", "Invalid name", nil)
			return
		}
		actor := gamesyncActorFor(issued.ticket)
		logger.Printf("Gamesync GetDocument: path_schema=%q id_matches_usid=%t", documentPathShape(name), resourceLeaf(name) == actor.usid)
		doc, rerr := s.gamesyncRoom(issued.ticket).readDocument(actor, name)
		if rerr != nil {
			grpcStatus(w, rerr.code, rerr.message, nil)
			return
		}
		grpcStatus(w, "0", "", doc)
	}
}

func resourceLeaf(name string) string {
	return name[strings.LastIndexByte(name, '/')+1:]
}

func gamesyncTargetChange(name string, state uint64) []byte {
	// Target.name is a path; notifications use only its final ID component.
	change := protoBytes(nil, 1, []byte(resourceLeaf(name)))
	change = protoVarint(change, 2, state)
	return protoBytes(nil, 3, change)
}

func gamesyncTargetFailure(name string, code uint64, message string) [][]byte {
	cause := protoVarint(nil, 1, code)
	cause = protoBytes(cause, 2, []byte(message))
	change := protoBytes(nil, 1, []byte(resourceLeaf(name)))
	change = protoVarint(change, 2, 4) // FAILED
	change = protoBytes(change, 3, cause)
	return [][]byte{protoBytes(nil, 3, change)}
}
