package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

const gamesyncListDocumentsPath = "/nn.npln.gamesync.v1.Gamesync/ListDocuments"

type gsListRequest struct {
	parent, cursor string
	size           int
	mask           []string
	showMissing    bool
}

func parseGSListRequest(payload []byte) (gsListRequest, *gsError) {
	q := gsListRequest{size: 100}
	err := visitProto(payload, func(f, w, n uint64, v []byte) error {
		switch f {
		case 1, 3:
			if w != 2 {
				return errors.New("Invalid string")
			}
			if f == 1 {
				q.parent = string(v)
			} else {
				q.cursor = string(v)
			}
		case 2:
			if w != 0 || n > 1<<31-1 {
				return errors.New("Invalid page_size")
			}
			if n > 0 {
				q.size = int(n)
			}
		case 4:
			if w != 2 {
				return errors.New("Invalid mask")
			}
			return visitProto(v, func(mf, mw, _ uint64, mv []byte) error {
				if mf != 1 {
					return nil
				}
				if mw != 2 || len(q.mask) >= gsMaxMaskPaths {
					return errors.New("Invalid mask")
				}
				q.mask = append(q.mask, string(mv))
				return nil
			})
		case 5:
			if w != 0 || n > 1 {
				return errors.New("Invalid show_missing")
			}
			q.showMissing = n == 1
		}
		return nil
	})
	if err != nil || !validGSListParent(q.parent) || len(q.cursor) > 2048 {
		return q, gsFail("3", "Invalid list request")
	}
	if q.size > 100 {
		q.size = 100
	}
	for _, path := range q.mask {
		if path == "*" {
			continue
		}
		if path == "" || len(path) > gsMaxSegment || strings.ContainsRune(path, 0) {
			return q, gsFail("3", "Invalid read mask")
		}
		if strings.ContainsAny(path, ".`") {
			return q, gsFail("12", "Nested mask not implemented")
		}
	}
	if q.showMissing {
		return q, gsFail("12", "Missing-document listing not implemented")
	}
	return q, nil
}

func validGSListParent(parent string) bool {
	parts := strings.Split(parent, "/")
	if len(parent) > gsMaxPathBytes || parts[0] != "docs" {
		return false
	}
	for _, part := range parts {
		if !gsSafeSegment(part) {
			return false
		}
	}
	// Direct collections and known presence collections only; never recursive.
	return len(parts) == 1 || len(parts) == 2 || (len(parts) == 4 && gsIsCollection(classifyGamesyncPath(parent).kind))
}

// Signed cursor bound to this room, user, collection and mask. It contains
// no documents and is never printed. Pagination uses lexical order without
// a persistent snapshot between calls (new writes can change the listing).
func (room *gamesyncRoom) listCursor(actor gamesyncActor, q gsListRequest, last string) string {
	data := []byte(actor.usid + "\x00" + q.parent + "\x00" + strings.Join(q.mask, "\x00") + "\x00" + last)
	mac := hmac.New(sha256.New, room.secret[:])
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(append(mac.Sum(nil), data...))
}

func (room *gamesyncRoom) listDocuments(actor gamesyncActor, q gsListRequest) ([]byte, int, *gsError) {
	room.mu.Lock()
	defer room.mu.Unlock()
	var last string
	if q.cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(q.cursor)
		if err != nil || len(raw) < 32 {
			return nil, 0, gsFail("3", "Invalid cursor")
		}
		mac := hmac.New(sha256.New, room.secret[:])
		mac.Write(raw[32:])
		prefix := actor.usid + "\x00" + q.parent + "\x00" + strings.Join(q.mask, "\x00") + "\x00"
		if !hmac.Equal(raw[:32], mac.Sum(nil)) || !strings.HasPrefix(string(raw[32:]), prefix) {
			return nil, 0, gsFail("3", "Unauthorized or invalid cursor")
		}
		last = strings.TrimPrefix(string(raw[32:]), prefix)
	}
	var names []string
	prefix := q.parent + "/"
	for name := range room.docs {
		if name <= last || !strings.HasPrefix(name, prefix) || strings.Contains(name[len(prefix):], "/") {
			continue
		}
		if room.authorizeRead(actor, classifyGamesyncPath(name)) == nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	count := len(names)
	if count > q.size {
		count = q.size
	}
	var response []byte
	for _, name := range names[:count] {
		doc := room.docs[name]
		if len(q.mask) > 0 && !gsInMask(q.mask, "*") {
			masked := doc.clone()
			masked.fields = nil
			for _, field := range doc.fields {
				if gsInMask(q.mask, field.key) {
					masked.fields = append(masked.fields, field)
				}
			}
			doc = masked
		}
		response = protoBytes(response, 1, encodeGamesyncDocument(name, doc))
	}
	if count < len(names) {
		response = protoBytes(response, 2, []byte(room.listCursor(actor, q, names[count-1])))
	}
	return response, count, nil
}

func (s *ticketStore) listGamesyncDocuments(logger *log.Logger) http.HandlerFunc {
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
		q, perr := parseGSListRequest(payload)
		logger.Printf("Gamesync ListDocuments: parent_schema=%q mask_fields=%d cursor_present=%t show_missing=%t", documentPathShape(q.parent), len(q.mask), q.cursor != "", q.showMissing)
		if perr != nil {
			grpcStatus(w, perr.code, perr.message, nil)
			return
		}
		response, count, lerr := s.gamesyncRoom(issued.ticket).listDocuments(gamesyncActorFor(issued.ticket), q)
		if lerr != nil {
			grpcStatus(w, lerr.code, lerr.message, nil)
			return
		}
		logger.Printf("Gamesync ListDocuments: documents=%d actual_listing=true", count)
		grpcStatus(w, "0", "", response)
	}
}
