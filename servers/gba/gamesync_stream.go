package main

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const gamesyncKeepUserSessionPath = "/nn.npln.gamesync.v1.Gamesync/KeepUserSession"

// Read one frame without waiting for EOF: the client keeps this body open.
// Check size before allocating memory; compression is unsupported.
func readGRPCStreamFrame(r io.Reader) ([]byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[1:])
	if header[0] != 0 || n > 64*1024 {
		return nil, errors.New("Invalid stream frame")
	}
	payload := make([]byte, int(n))
	_, err := io.ReadFull(r, payload)
	return payload, err
}

type keepSessionRequest struct {
	name string
	kind uint64
	data []byte
}

func parseKeepSessionRequest(payload []byte) (keepSessionRequest, error) {
	var req keepSessionRequest
	seenName := false
	err := visitProto(payload, func(field, wire, _ uint64, value []byte) error {
		if field < 1 || field > 4 {
			return nil
		}
		if wire != 2 {
			return errors.New("Invalid KeepUserSession field")
		}
		if field == 1 {
			if seenName {
				return errors.New("Duplicate name")
			}
			seenName = true
			req.name = string(value)
		} else {
			if req.kind != 0 {
				return errors.New("Multiple KeepUserSession operations")
			}
			req.kind, req.data = field, value
		}
		return nil
	})
	if err == nil && (req.name == "" || req.kind == 0) {
		err = errors.New("Incomplete KeepUserSession")
	}
	return req, err
}

// Log only known schema labels, without UUIDs, tokens,
// player names or document values.
func documentPathShape(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) > 16 {
		return "long-path"
	}
	for i, part := range parts {
		switch part {
		case "tenants", "current", "gameSessions", "userSessions", "targets", "streams", "keepUserSession", "docs", "__gs", "__us", "__pgn", "__pus", "__stu", "__stg", "All", "f", "m", "r", "n", "ck", "s":
		default:
			parts[i] = "{id}"
		}
	}
	return strings.Join(parts, "/")
}

type gamesyncTarget struct {
	name  string
	kind  uint64
	paths []string
}

func parseGamesyncTarget(data []byte) (gamesyncTarget, error) {
	var result gamesyncTarget
	var target []byte
	err := visitProto(data, func(f, w, _ uint64, v []byte) error {
		if f == 1 {
			if w != 2 || target != nil {
				return errors.New("Invalid target")
			}
			target = v
		}
		return nil
	})
	if err != nil || target == nil {
		return result, errors.New("Missing target")
	}
	var name string
	var kind uint64
	var selector []byte
	err = visitProto(target, func(f, w, _ uint64, v []byte) error {
		if f >= 1 && f <= 3 {
			if w != 2 {
				return errors.New("Invalid target")
			}
			if f == 1 {
				name = string(v)
			} else {
				if kind != 0 {
					return errors.New("Multiple target types")
				}
				kind, selector = f, v
			}
		}
		return nil
	})
	if err != nil || name == "" || kind == 0 {
		return result, errors.New("Incomplete target")
	}
	count := 0
	err = visitProto(selector, func(f, w, _ uint64, v []byte) error {
		if f == 1 {
			if w != 2 || len(v) == 0 {
				return errors.New("Invalid selector")
			}
			count++
			if count > 16 {
				return errors.New("Too many target references")
			}
			result.paths = append(result.paths, string(v))
		}
		return nil
	})
	if err != nil || count == 0 {
		return result, errors.New("Empty selector")
	}
	result.name, result.kind = name, kind
	return result, nil
}

func inspectGamesyncTarget(data []byte, logger *log.Logger) (string, error) {
	target, err := parseGamesyncTarget(data)
	if err != nil {
		return "", err
	}
	logGamesyncTarget(target, logger)
	return target.name, nil
}

func logGamesyncTarget(target gamesyncTarget, logger *log.Logger) {
	for _, path := range target.paths {
		logger.Printf("Gamesync requested target: type=%d path_schema=%q", target.kind, documentPathShape(path))
	}
	logger.Printf("Gamesync target: type=%d references=%d name_schema=%q name_length=%d target_id_length=%d", target.kind, len(target.paths), documentPathShape(target.name), len(target.name), len(resourceLeaf(target.name)))
}

func (s *ticketStore) keepGamesyncSession(logger *log.Logger) http.HandlerFunc {
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
			logger.Print("Gamesync KeepUserSession: authorization_bearer=false")
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
		// Derive the role from the authenticated ticket, never from client input.
		role := "unclassified"
		s.mu.Lock()
		if session := s.sessions[issued.ticket.sessionName]; session != nil {
			role = "guest"
			if session.host.owner == issued.ticket.owner {
				role = "host"
			}
		}
		s.mu.Unlock()
		logger := log.New(logger.Writer(), logger.Prefix()+"[participant="+role+"] ", logger.Flags())
		logger.Print("Gamesync KeepUserSession: local_token_recognized=true stream_open=true")
		// Room and permissions come only from the exact token, never from client
		// input in channel or document names.
		room := s.gamesyncRoom(issued.ticket)
		actor := gamesyncActorFor(issued.ticket)
		ch := room.openChannel(actor)
		// One writer per channel: responses and document changes from other
		// channels or writes use the same queue.
		written := make(chan struct{})
		go func() {
			defer close(written)
			for {
				frames, open := ch.q.next()
				if !open {
					return
				}
				for _, frame := range frames {
					if writeGRPCFrame(w, frame) != nil {
						ch.q.close()
						return
					}
				}
			}
		}()
		// Stop the writer, release watches and execute deferments for the last
		// channel. Then write the final status without competing writers.
		finish := func(status, message string) {
			logger.Printf("Gamesync closure: trailer_status=%q context_canceled=%t", status, r.Context().Err() != nil)
			ch.q.close()
			<-written
			room.closeChannel(ch, logger)
			if status == "" {
				// Finish with trailers; do not send an additional empty message.
				w.Header().Set("Grpc-Status", "0")
				return
			}
			grpcStatus(w, status, message, nil)
		}
		for {
			payload, err := readGRPCStreamFrame(r.Body)
			if err == io.EOF {
				logger.Print("Gamesync read ended: reason=request_EOF")
				finish("", "")
				return
			}
			if err != nil {
				if r.Context().Err() != nil {
					logger.Print("Gamesync KeepUserSession: client_disconnected=true")
					ch.q.close()
					<-written
					room.closeChannel(ch, logger)
					return
				}
				finish("3", "Invalid KeepUserSession frame")
				return
			}
			req, err := parseKeepSessionRequest(payload)
			if err != nil {
				finish("3", "Invalid KeepUserSession")
				return
			}
			form, matches := matchingGamesyncSession(req.name, issued.ticket.userName)
			if req.name == "userSessions/current" {
				// The exact token already selected issued.ticket: current is never
				// resolved through a global last session or another player.
				form, matches = "current-token", true
			}
			logger.Printf("Gamesync KeepUserSession reference: format=%s path_schema=%q length=%d segments=%d operation=%d matches=%t", form, documentPathShape(req.name), len(req.name), len(strings.Split(req.name, "/")), req.kind, matches)
			if !time.Now().Before(issued.expires) {
				finish("16", "Local Gamesync token expired")
				return
			}
			// Echo returns only bytes from the same authenticated request.
			// It neither reads nor modifies sessions, so the channel name need not
			// be resolved as a resource reference. Target operations still verify
			// the participant bound to the token.
			if req.kind != 2 && !matches {
				if req.kind == 3 {
					// Collecting only the requested structure before rejection allows
					// format diagnostics without accessing documents.
					_, _ = inspectGamesyncTarget(req.data, logger)
				}
				logger.Printf("Gamesync KeepUserSession: reference=%s matches=false", form)
				finish("16", "Invalid local Gamesync session")
				return
			}
			switch req.kind {
			case 2:
				logger.Printf("Gamesync KeepUserSession: echo_received=true bytes=%d", len(req.data))
				ch.q.push(protoBytes(nil, 1, req.data))
				logger.Print("Gamesync KeepUserSession: echo_sent=true")
			case 3:
				target, err := parseGamesyncTarget(req.data)
				if err != nil {
					finish("3", "Invalid target")
					return
				}
				logGamesyncTarget(target, logger)
				room.installWatch(ch, actor, target, logger)
			case 4:
				name, err := protoStringField1(req.data)
				if err != nil || resourceLeaf(name) == "" {
					finish("3", "Invalid DeleteTarget")
					return
				}
				room.removeWatch(ch, name, logger)
			}
		}
	}
}
