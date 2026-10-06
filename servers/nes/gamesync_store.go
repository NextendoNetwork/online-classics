package main

import (
	"crypto/rand"
	"errors"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// In-memory Gamesync store, one per room (the ticket's GameSession), bound
// to the exact access token issued by IssueToken. Nothing is shared between
// rooms: documents, globals, deferments and watches belong to the room.
//
// Known behavior is supported by Genesis traces and kinnay/NPLN-Protocols schemas.
// Unconfirmed behavior is explicitly marked HYPOTHESIS in the comments.

const (
	gsMaxDocFields   = 128
	gsMaxDocBytes    = 48 * 1024
	gsMaxRoomDocs    = 512
	gsMaxGlobals     = 64
	gsMaxOps         = 32
	gsMaxTransforms  = 16
	gsMaxDeferments  = 16
	gsMaxMaskPaths   = 32
	gsMaxPathBytes   = 256
	gsMaxSegment     = 128
	gsMaxDeferOps    = 8
	gsMaxDefermentsP = 16 // Per participant.
)

// HYPOTHESIS (default false): Firestore-style FieldMask. Only masked fields
// are written; a masked field missing from the document is deleted.
// The Nextendo reference (Splatoon 3) appears to merge all fields regardless
// of the mask; true also writes unmasked fields.
var gamesyncMaskedUpdateStoresUnmasked = false

type gsError struct{ code, message string }

func (e *gsError) Error() string { return e.message }

func gsFail(code, message string) *gsError { return &gsError{code: code, message: message} }

func (e *gsError) codeNumber() uint64 {
	n, err := strconv.ParseUint(e.code, 10, 64)
	if err != nil {
		return 13
	}
	return n
}

// ---------------------------------------------------------------- documents

type gsField struct {
	key   string
	value []byte // Encoded common.Value.
}

type gsDoc struct {
	signalingOwner string // Authorized sender identity; never serialized.
	fields         []gsField
	created        time.Time
	updated        time.Time
}

func (d *gsDoc) clone() *gsDoc {
	c := &gsDoc{signalingOwner: d.signalingOwner, created: d.created, updated: d.updated, fields: make([]gsField, len(d.fields))}
	copy(c.fields, d.fields) // Values are immutable.
	return c
}

func (d *gsDoc) get(key string) ([]byte, bool) {
	for _, f := range d.fields {
		if f.key == key {
			return f.value, true
		}
	}
	return nil, false
}

func (d *gsDoc) set(key string, value []byte) {
	for i := range d.fields {
		if d.fields[i].key == key {
			d.fields[i].value = value
			return
		}
	}
	d.fields = append(d.fields, gsField{key: key, value: value})
}

func (d *gsDoc) remove(key string) {
	for i := range d.fields {
		if d.fields[i].key == key {
			d.fields = append(d.fields[:i], d.fields[i+1:]...)
			return
		}
	}
}

func (d *gsDoc) size() int {
	n := 0
	for _, f := range d.fields {
		n += len(f.key) + len(f.value) + 8
	}
	return n
}

func protoTimestamp(t time.Time) []byte {
	stamp := protoVarint(nil, 1, uint64(t.Unix()))
	return protoVarint(stamp, 2, uint64(t.Nanosecond()))
}

func encodeGamesyncDocument(path string, d *gsDoc) []byte {
	var fields []byte
	for _, f := range d.fields {
		entry := protoBytes(nil, 1, []byte(f.key))
		entry = protoBytes(entry, 2, f.value)
		fields = protoBytes(fields, 1, entry)
	}
	doc := protoBytes(nil, 1, []byte(path))
	doc = protoBytes(doc, 2, fields)
	doc = protoBytes(doc, 3, protoTimestamp(d.created))
	return protoBytes(doc, 4, protoTimestamp(d.updated))
}

// Initial UserSession document fields from the actual ticket. Types follow
// common.Value; names follow userSessionFields in the public reference.
// Genesis reads of these fields still need verification.
func userSessionSeedFields(ticket pendingTicket, rank uint64) []gsField {
	team := ""
	attributes := []byte(nil)
	_ = visitProto(ticket.user, func(f, w, _ uint64, value []byte) error {
		if f == 2 && w == 2 {
			attributes = value
		}
		if f == 4 && w == 2 {
			team = string(value)
		}
		return nil
	})
	fields := []gsField{{"uid", protoBytes(nil, 7, []byte(ticket.owner))}}
	// Stable participant order within this room, shared by all three fields.
	for _, key := range []string{"ussid", "ucsid", "upcsid"} {
		fields = append(fields, gsField{key, protoVarint(nil, 3, rank)})
	}
	fields = append(fields,
		gsField{"pgn", protoBytes(nil, 7, []byte("All"))},
		gsField{"st", protoVarint(nil, 3, 2)}, // ACTIVE, as in the ticket's UserSession.
		gsField{"tn", protoBytes(nil, 7, []byte(team))},
		gsField{"att", protoBytes(nil, 10, attributes)},
		gsField{"ltc", protoBytes(nil, 10, nil)},
	)
	return fields
}

// ------------------------------------------------------------------- paths

type gsPathKind int

const (
	gsPathInvalid gsPathKind = iota
	gsPathUserSession
	gsPathPresence
	gsPathUserState
	gsPathStage
	gsPathGameDocument
	gsPathPresenceCollection
	gsPathStateCollection
	gsPathUnsupported
	gsPathFixedSession
	gsPathGameCollection
	gsPathUserSessionCollection
	gsPathFixedCollection
)

type gsPath struct {
	kind gsPathKind
	id   string
}

func gsSafeSegment(s string) bool {
	if s == "" || len(s) > gsMaxSegment {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '@' || r == '.' || r == '-' || r == ':' || r == '=' || r == '+':
		default:
			return false
		}
	}
	return true
}

// System families observed in Genesis: __us, __pgn/All/{__stu,__pus},
// __stg/All and __gs/f. Opaque game collections are stored without interpreting
// their fields, with permissions limited to the caller's USID. The __ prefix
// alone does not establish server ownership; known system roots are reserved.
func classifyGamesyncPath(p string) gsPath {
	if p == "" || len(p) > gsMaxPathBytes {
		return gsPath{}
	}
	parts := strings.Split(p, "/")
	if len(parts) < 2 || len(parts) > 6 || parts[0] != "docs" {
		return gsPath{}
	}
	for _, part := range parts {
		if !gsSafeSegment(part) {
			return gsPath{}
		}
	}
	switch {
	case len(parts) == 2 && parts[1] == "__us":
		return gsPath{kind: gsPathUserSessionCollection}
	case len(parts) == 2 && parts[1] == "__gs":
		return gsPath{kind: gsPathFixedCollection}
	case len(parts) == 2 && parts[1] != "__pgn" && parts[1] != "__stg":
		return gsPath{kind: gsPathGameCollection}
	case len(parts) == 3 && parts[1] == "__us":
		return gsPath{gsPathUserSession, parts[2]}
	case len(parts) == 5 && parts[1] == "__pgn" && parts[2] == "All" && parts[3] == "__pus":
		return gsPath{gsPathPresence, parts[4]}
	case len(parts) == 5 && parts[1] == "__pgn" && parts[2] == "All" && parts[3] == "__stu":
		return gsPath{gsPathUserState, parts[4]}
	case len(parts) == 4 && parts[1] == "__pgn" && parts[2] == "All" && parts[3] == "__pus":
		return gsPath{kind: gsPathPresenceCollection}
	case len(parts) == 4 && parts[1] == "__pgn" && parts[2] == "All" && parts[3] == "__stu":
		return gsPath{kind: gsPathStateCollection}
	case len(parts) == 3 && parts[1] == "__stg" && parts[2] == "All":
		return gsPath{kind: gsPathStage}
	case len(parts) == 3 && parts[1] == "__gs" && parts[2] == "f":
		return gsPath{kind: gsPathFixedSession}
	case len(parts) == 3 && parts[1] != "__gs" && parts[1] != "__us" && parts[1] != "__pgn" && parts[1] != "__stg":
		return gsPath{gsPathGameDocument, parts[2]}
	}
	return gsPath{kind: gsPathUnsupported}
}

func gsIsCollection(k gsPathKind) bool {
	return k == gsPathPresenceCollection || k == gsPathStateCollection || k == gsPathGameCollection || k == gsPathUserSessionCollection || k == gsPathFixedCollection
}

// Schema labels permitted in logs. Any other name (possibly an identifier
// or player data) is hidden.
var gsKnownLabels = map[string]bool{
	"uid": true, "ussid": true, "ucsid": true, "upcsid": true, "pgn": true, "st": true, "tn": true,
	"att": true, "ltc": true, "suid": true, "susid": true, "sussid": true, "suscid": true, "pl": true,
	"mp": true, "gsid": true, "addr": true, "p": true, "mcn": true, "maxu": true, "rs": true,
	"tid": true, "k": true, "ipc": true, "dpc": true,
	"__upcsidn": true, // Confirmed constant in Genesis main at 0x929631.
}

func gsLabel(name, replacement string) string {
	if gsKnownLabels[name] {
		return name
	}
	return replacement
}

// ------------------------------------------------------------------ actor

type gamesyncActor struct {
	usid  string
	gsid  string
	owner string
	ready bool // The ticket contains the data needed to seed its UserSession.
}

func gamesyncActorFor(ticket pendingTicket) gamesyncActor {
	return gamesyncActor{
		usid:  resourceLeaf(ticket.userName),
		gsid:  resourceLeaf(ticket.sessionName),
		owner: ticket.owner,
		ready: !ticket.createdAt.IsZero() && ticket.owner != "",
	}
}

// ------------------------------------------------------------ frame queue

// One writer per channel: all client-bound responses and notifications pass
// through this queue, drained by a single goroutine.
type frameQueue struct {
	mu     sync.Mutex
	frames [][]byte
	closed bool
	wake   chan struct{}
}

func newFrameQueue() *frameQueue { return &frameQueue{wake: make(chan struct{}, 1)} }

func (q *frameQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *frameQueue) push(frames ...[]byte) {
	q.mu.Lock()
	if !q.closed {
		q.frames = append(q.frames, frames...)
	}
	q.mu.Unlock()
	q.signal()
}

func (q *frameQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	q.signal()
}

// Returns queued frames; false when the queue is closed and empty.
func (q *frameQueue) next() ([][]byte, bool) {
	for {
		q.mu.Lock()
		if len(q.frames) > 0 {
			frames := q.frames
			q.frames = nil
			q.mu.Unlock()
			return frames, true
		}
		if q.closed {
			q.mu.Unlock()
			return nil, false
		}
		q.mu.Unlock()
		<-q.wake
	}
}

// ------------------------------------------------------------------- room

type gsWatch struct {
	name       string
	id         string
	kind       uint64 // 2: document, 3: collection.
	paths      map[string]bool
	collection string
}

func (wt *gsWatch) matches(path string) bool {
	if wt.kind == 2 {
		return wt.paths[path]
	}
	prefix := wt.collection + "/"
	return strings.HasPrefix(path, prefix) && !strings.Contains(path[len(prefix):], "/")
}

type gamesyncChannel struct {
	room    *gamesyncRoom
	usid    string
	q       *frameQueue
	watches map[string]*gsWatch // Protected by room.mu.
}

type gamesyncRoom struct {
	seedUserState       bool
	diagnosticSnapshots int
	onLeave             func(string)
	secret              [32]byte
	mu                  sync.Mutex
	docs                map[string]*gsDoc
	globals             map[string][]byte
	participants        map[string]bool
	participantRanks    map[string]uint64
	lastParticipantRank uint64
	deferments          map[string]map[string][]gsWrite // usid -> name -> cleanup writes.
	channels            map[*gamesyncChannel]struct{}
	open                map[string]int
}

func newGamesyncRoom() *gamesyncRoom {
	room := &gamesyncRoom{
		docs:             make(map[string]*gsDoc),
		globals:          make(map[string][]byte),
		participants:     make(map[string]bool),
		participantRanks: make(map[string]uint64),
		deferments:       make(map[string]map[string][]gsWrite),
		channels:         make(map[*gamesyncChannel]struct{}),
		open:             make(map[string]int),
	}
	if _, err := rand.Read(room.secret[:]); err != nil {
		panic(err)
	}
	return room
}

// One room per ticket GameSession. Each participant uses their own token;
// a second ticket for the same session registers in the same room.
// Historical J2 preparation: JoinGameSession was not yet implemented at this stage.
func (s *ticketStore) gamesyncRoom(ticket pendingTicket) *gamesyncRoom {
	key := ticket.sessionName
	if key == "" {
		key = ticket.userName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rooms == nil {
		s.rooms = make(map[string]*gamesyncRoom)
	}
	room := s.rooms[key]
	if room == nil {
		room = newGamesyncRoom()
		// Settings belong to the host even when the guest queries Gamesync first.
		// Do not replace them with settings from a join ticket.
		seed := ticket
		if session := s.sessions[key]; session != nil {
			seed = session.host
		}
		room.seedUserState = s.genesisUserState && resourceLeaf(seed.config) == "LCLA6-2P"
		if s.genesisStage && resourceLeaf(seed.config) == "LCLA6-2P" && seed.owner != "" && !seed.createdAt.IsZero() {
			room.docs["docs/__stg/All"] = &gsDoc{fields: experimentalStageFields(seed), created: seed.createdAt, updated: seed.createdAt}
		}
		room.onLeave = func(usid string) { s.leaveMatchSession(key, usid) }
		s.rooms[key] = room
	}
	room.register(ticket)
	return room
}

func (room *gamesyncRoom) register(ticket pendingTicket) {
	usid := resourceLeaf(ticket.userName)
	if usid == "" {
		return
	}
	room.mu.Lock()
	defer room.mu.Unlock()
	if room.participants[usid] && (ticket.createdAt.IsZero() || ticket.owner == "" || room.docs["docs/__us/"+usid] != nil) {
		return
	}
	room.participants[usid] = true
	if !ticket.createdAt.IsZero() && ticket.owner != "" {
		rank := room.participantRanks[usid]
		if rank == 0 {
			room.lastParticipantRank++
			rank = room.lastParticipantRank
			room.participantRanks[usid] = rank
		}
		room.docs["docs/__us/"+usid] = &gsDoc{fields: userSessionSeedFields(ticket, rank), created: ticket.createdAt, updated: ticket.createdAt}
		room.notifyLocked([]gsChange{{path: "docs/__us/" + usid, doc: room.docs["docs/__us/"+usid]}})
		if room.docs["docs/__gs/f"] == nil {
			room.docs["docs/__gs/f"] = &gsDoc{fields: fixedSessionFields(ticket, room.secret[:]), created: ticket.createdAt, updated: ticket.createdAt}
		}
		// Bounded HYPOTHESIS: initial state before the client publishes pl.
		// Actual identity and empty bytes only; do not copy sequences from another game.
		// Never replace existing data or regenerate it during subsequent reads.
		statePath := "docs/__pgn/All/__stu/" + usid
		if room.seedUserState && room.docs[statePath] == nil {
			stateDoc := &gsDoc{fields: []gsField{
				{"suid", protoBytes(nil, 7, []byte(ticket.owner))},
				{"susid", protoBytes(nil, 7, []byte(usid))},
				{"pl", protoBytes(nil, 8, nil)},
			}, created: ticket.createdAt, updated: ticket.createdAt}
			room.docs[statePath] = stateDoc
			room.notifyLocked([]gsChange{{path: statePath, doc: stateDoc}})
		}
	}
}

func (room *gamesyncRoom) openChannel(actor gamesyncActor) *gamesyncChannel {
	ch := &gamesyncChannel{room: room, usid: actor.usid, q: newFrameQueue(), watches: make(map[string]*gsWatch)}
	room.mu.Lock()
	room.channels[ch] = struct{}{}
	room.open[actor.usid]++
	room.mu.Unlock()
	return ch
}

// Closing a participant's last channel executes their deferments.
// HYPOTHESIS: a deferment is a deferred cleanup write (observed: deletion
// of the caller's own document) executed when the participant loses their
// KeepUserSession. No public reference covers this operation.
func (room *gamesyncRoom) closeChannel(ch *gamesyncChannel, logger *log.Logger) {
	room.mu.Lock()
	departed := false
	defer func() {
		room.mu.Unlock()
		if departed && room.onLeave != nil {
			room.onLeave(ch.usid)
		}
	}()
	delete(room.channels, ch)
	ch.watches = make(map[string]*gsWatch)
	room.open[ch.usid]--
	if room.open[ch.usid] > 0 {
		return
	}
	delete(room.open, ch.usid)
	departed = true
	// Retain membership history to read documents left after canceling a deferment.
	// open and the matchmaking registry determine active slots;
	// the departing participant's token is revoked.
	var removed []gsChange
	for _, path := range []string{"docs/__us/" + ch.usid, "docs/__pgn/All/__stu/" + ch.usid} {
		if doc := room.docs[path]; doc != nil && (doc.signalingOwner == "" || doc.signalingOwner == ch.usid) {
			delete(room.docs, path)
			removed = append(removed, gsChange{path: path})
		}
	}
	// Signals use opaque keys and the observed publications have no deferment.
	// Retaining them binds reused keys to the departed USID and rejects a
	// reconnecting player. Remove only messages owned by this last channel's
	// sender; closing one of several channels must retain the live session.
	var signalPaths []string
	for path, doc := range room.docs {
		if classifyGamesyncPath(path).kind == gsPathUserState && doc.signalingOwner == ch.usid {
			signalPaths = append(signalPaths, path)
		}
	}
	sort.Strings(signalPaths)
	for _, path := range signalPaths {
		delete(room.docs, path)
		removed = append(removed, gsChange{path: path})
	}
	if len(signalPaths) > 0 {
		logger.Printf("Gamesync signaling cleanup: documents=%d last_channel=true", len(signalPaths))
	}
	room.notifyLocked(removed)
	defs := room.deferments[ch.usid]
	delete(room.deferments, ch.usid)
	if len(defs) == 0 {
		return
	}
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)
	var writes []gsWrite
	for _, name := range names {
		writes = append(writes, defs[name]...)
	}
	_, _, err := room.applyLocked(gamesyncActor{usid: ch.usid}, writes, nil, time.Now().UTC())
	logger.Printf("Gamesync deferment cleanup: names=%d operations=%d applied=%t semantics=hypothesis", len(names), len(writes), err == nil)
}

// ---------------------------------------------------------- authorization

func (room *gamesyncRoom) authorizeRead(actor gamesyncActor, p gsPath) *gsError {
	switch p.kind {
	case gsPathUserSession:
		if p.id != actor.usid && !room.participants[p.id] {
			return gsFail("7", "Another participant's document")
		}
		if !actor.ready {
			return gsFail("13", "Incomplete local ticket")
		}
	case gsPathUserState:
		// __stu uses message keys, not participant IDs. The store is scoped
		// to this GameSession and only its registered peers can read signals.
		if !room.participants[actor.usid] {
			return gsFail("7", "Signaling reader outside the room")
		}
	case gsPathPresence:
		if p.id != actor.usid && !room.participants[p.id] {
			return gsFail("7", "Participant outside the room")
		}
	case gsPathGameDocument:
		if p.id != actor.usid && !room.participants[p.id] {
			return gsFail("7", "Another participant's document")
		}
	case gsPathFixedSession:
		if !actor.ready {
			return gsFail("13", "Incomplete local ticket")
		}
	case gsPathStage, gsPathPresenceCollection, gsPathStateCollection, gsPathGameCollection, gsPathUserSessionCollection, gsPathFixedCollection:
	default:
		return gsFail("12", "Gamesync document not implemented")
	}
	return nil
}

func gsAuthorizeWrite(actor gamesyncActor, p gsPath) *gsError {
	if actor.usid == "" {
		return gsFail("16", "Participant has no identity")
	}
	switch p.kind {
	case gsPathPresence, gsPathUserState, gsPathGameDocument:
		if p.id != actor.usid {
			return gsFail("7", "Write to another participant's document")
		}
		return nil
	case gsPathUserSession, gsPathStage, gsPathFixedSession:
		return gsFail("12", "Unobserved system-document write")
	case gsPathPresenceCollection, gsPathStateCollection, gsPathGameCollection, gsPathUserSessionCollection, gsPathFixedCollection, gsPathInvalid:
		return gsFail("3", "Invalid write path")
	}
	return gsFail("12", "Gamesync document not implemented")
}

// ------------------------------------------------------------- values

func gsValueKind(raw []byte) (uint64, *gsError) {
	var kind uint64
	err := visitProto(raw, func(f, w, n uint64, v []byte) error {
		if kind != 0 {
			return errors.New("Value contains multiple types")
		}
		switch f {
		case 1, 3:
			if w != 0 {
				return errors.New("Invalid value type")
			}
		case 2:
			if w != 0 || n > 1 {
				return errors.New("Invalid boolean")
			}
		case 4:
			if w != 5 {
				return errors.New("Invalid float")
			}
		case 5:
			if w != 1 {
				return errors.New("Invalid double")
			}
		case 6, 9, 10:
			if w != 2 || visitProto(v, func(_, _, _ uint64, _ []byte) error { return nil }) != nil {
				return errors.New("Invalid composite value")
			}
		case 7, 11:
			if w != 2 || !utf8.Valid(v) {
				return errors.New("Invalid string")
			}
		case 8:
			if w != 2 {
				return errors.New("Invalid bytes")
			}
		default:
			return errors.New("Unknown value type")
		}
		kind = f
		return nil
	})
	if err != nil || kind == 0 {
		return 0, gsFail("3", "Invalid common.Value")
	}
	return kind, nil
}

// Numeric common.Value: integer (int64) or floating-point value.
func gsNumber(raw []byte) (isInt bool, i int64, f float64, ok bool) {
	_ = visitProto(raw, func(field, wire, n uint64, v []byte) error {
		switch {
		case field == 3 && wire == 0:
			isInt, i, ok = true, int64(n), true
		case field == 4 && wire == 5:
			f, ok = float64(math.Float32frombits(leUint32(v))), true
		case field == 5 && wire == 1:
			f, ok = math.Float64frombits(leUint64(v)), true
		}
		return nil
	})
	return
}

func leUint32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func leUint64(b []byte) uint64 {
	return uint64(leUint32(b)) | uint64(leUint32(b[4:]))<<32
}

func gsIntValue(i int64) []byte { return protoVarint(nil, 3, uint64(i)) }

func gsDoubleValue(f float64) []byte {
	bits := math.Float64bits(f)
	out := []byte{(5 << 3) | 1}
	for shift := 0; shift < 64; shift += 8 {
		out = append(out, byte(bits>>shift))
	}
	return out
}

var gsNullValue = protoVarint(nil, 1, 0)

// ---------------------------------------------------------------- requests

type gsTransform struct {
	path    string
	kind    uint64 // 3 increment, 4 maximum, 6 load_global_value, 7 store_global_value (others unobserved).
	operand []byte
	global  string
}

type gsWrite struct {
	kind       uint64 // 1 update, 2 merge, 3 transform, 4 delete
	path       string
	fields     []gsField
	mask       []string
	precond    bool
	transforms []gsTransform
}

type gsDeferment struct {
	update bool
	name   string
	ops    []gsWrite
}

func gsParseDocument(data []byte) (string, []gsField, *gsError) {
	var path string
	var fields []gsField
	seen := map[string]bool{}
	err := visitProto(data, func(f, w, _ uint64, v []byte) error {
		switch {
		case f == 1:
			if w != 2 {
				return errors.New("Invalid name")
			}
			path = string(v)
		case f == 2:
			if w != 2 {
				return errors.New("Invalid fields")
			}
			return visitProto(v, func(ef, ew, _ uint64, entry []byte) error {
				if ef != 1 {
					return nil
				}
				if ew != 2 {
					return errors.New("Invalid map entry")
				}
				var key string
				var value []byte
				hasKey, hasValue := false, false
				if err := visitProto(entry, func(kf, kw, _ uint64, kv []byte) error {
					if kw != 2 {
						return errors.New("Invalid map entry")
					}
					if kf == 1 {
						key, hasKey = string(kv), true
					} else if kf == 2 {
						value, hasValue = kv, true
					}
					return nil
				}); err != nil {
					return err
				}
				if !hasKey || !hasValue || key == "" || len(key) > gsMaxSegment || seen[key] {
					return errors.New("Invalid document field")
				}
				if _, verr := gsValueKind(value); verr != nil {
					return errors.New(verr.message)
				}
				seen[key] = true
				fields = append(fields, gsField{key, append([]byte(nil), value...)})
				if len(fields) > gsMaxDocFields {
					return errors.New("Too many fields")
				}
				return nil
			})
		}
		return nil
	})
	if err != nil || path == "" {
		return "", nil, gsFail("3", "Invalid write document")
	}
	return path, fields, nil
}

func gsParseTransform(data []byte) (gsTransform, *gsError) {
	var t gsTransform
	err := visitProto(data, func(f, w, _ uint64, v []byte) error {
		if f == 1 {
			if w != 2 {
				return errors.New("Invalid transform field")
			}
			t.path = string(v)
			return nil
		}
		if f < 2 || f > 8 {
			return nil
		}
		if t.kind != 0 {
			return errors.New("Duplicate transform oneof")
		}
		t.kind = f
		switch f {
		case 2:
			if w != 0 {
				return errors.New("Invalid transform")
			}
		case 3, 4, 5:
			if w != 2 {
				return errors.New("Invalid operand")
			}
			if _, verr := gsValueKind(v); verr != nil {
				return errors.New(verr.message)
			}
			t.operand = append([]byte(nil), v...)
		default:
			if w != 2 || !utf8.Valid(v) || len(v) == 0 || len(v) > gsMaxSegment {
				return errors.New("Invalid global value")
			}
			t.global = string(v)
		}
		return nil
	})
	if err != nil || t.path == "" || t.kind == 0 {
		return t, gsFail("3", "Invalid transform")
	}
	return t, nil
}

func gsParseWriteOperation(data []byte) (gsWrite, *gsError) {
	var wr gsWrite
	var body []byte
	err := visitProto(data, func(f, w, _ uint64, v []byte) error {
		if f < 1 || f > 4 {
			return nil
		}
		if w != 2 || wr.kind != 0 {
			return errors.New("Invalid oneof")
		}
		wr.kind, body = f, v
		return nil
	})
	if err != nil || wr.kind == 0 {
		return wr, gsFail("3", "Invalid write operation")
	}
	switch wr.kind {
	case 1:
		var doc, mask []byte
		err = visitProto(body, func(f, w, _ uint64, v []byte) error {
			if f >= 1 && f <= 3 && w != 2 {
				return errors.New("Invalid update")
			}
			switch f {
			case 1:
				doc = v
			case 2:
				mask = v
			case 3:
				wr.precond = true
			}
			return nil
		})
		if err != nil {
			return wr, gsFail("3", "Invalid update_document")
		}
		var perr *gsError
		if wr.path, wr.fields, perr = gsParseDocument(doc); perr != nil {
			return wr, perr
		}
		err = visitProto(mask, func(f, w, _ uint64, v []byte) error {
			if f == 1 && w == 2 {
				wr.mask = append(wr.mask, string(v))
				if len(wr.mask) > gsMaxMaskPaths {
					return errors.New("Mask too long")
				}
			}
			return nil
		})
		if err != nil {
			return wr, gsFail("3", "Invalid mask")
		}
	case 2:
		return wr, gsFail("12", "merge_document not observed")
	case 3:
		err = visitProto(body, func(f, w, _ uint64, v []byte) error {
			if w != 2 {
				return nil
			}
			if f == 1 {
				wr.path = string(v)
			}
			if f == 2 {
				t, terr := gsParseTransform(v)
				if terr != nil {
					return errors.New(terr.message)
				}
				wr.transforms = append(wr.transforms, t)
				if len(wr.transforms) > gsMaxTransforms {
					return errors.New("Too many transforms")
				}
			}
			return nil
		})
		if err != nil || wr.path == "" || len(wr.transforms) == 0 {
			return wr, gsFail("3", "Invalid transform_document")
		}
	case 4:
		err = visitProto(body, func(f, w, _ uint64, v []byte) error {
			if f == 1 && w == 2 {
				wr.path = string(v)
			}
			if f == 2 {
				wr.precond = true
			}
			return nil
		})
		if err != nil || wr.path == "" {
			return wr, gsFail("3", "Invalid delete_document")
		}
	}
	return wr, nil
}

func gsParseDeferment(data []byte) (gsDeferment, *gsError) {
	var d gsDeferment
	var kind uint64
	var body []byte
	err := visitProto(data, func(f, w, _ uint64, v []byte) error {
		if f != 1 && f != 2 {
			return nil
		}
		if w != 2 || kind != 0 {
			return errors.New("Invalid deferment")
		}
		kind, body = f, v
		return nil
	})
	if err != nil || kind == 0 {
		return d, gsFail("3", "Invalid deferment")
	}
	d.update = kind == 1
	err = visitProto(body, func(f, w, _ uint64, v []byte) error {
		if f != 1 || w != 2 {
			return nil
		}
		if !d.update {
			d.name = string(v)
			return nil
		}
		// UpdateDefermentRequest.deferment = Deferment{name, write_operations}.
		return visitProto(v, func(sf, sw, _ uint64, sv []byte) error {
			if sw != 2 {
				return nil
			}
			if sf == 1 {
				d.name = string(sv)
			}
			if sf == 2 {
				op, oerr := gsParseWriteOperation(sv)
				if oerr != nil {
					return errors.New(oerr.code + ":" + oerr.message)
				}
				d.ops = append(d.ops, op)
				if len(d.ops) > gsMaxDeferOps {
					return errors.New("Too many deferred writes")
				}
			}
			return nil
		})
	})
	if err != nil {
		if code, msg, found := strings.Cut(err.Error(), ":"); found && (code == "12" || code == "3") {
			return d, gsFail(code, "deferment: "+msg)
		}
		return d, gsFail("3", "Invalid deferment")
	}
	if !gsDefermentName(d.name) {
		return d, gsFail("3", "Invalid deferment name")
	}
	return d, nil
}

func gsDefermentName(name string) bool {
	if !strings.HasPrefix(name, "userSessions/current/") || len(name) > gsMaxPathBytes {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if !gsSafeSegment(part) {
			return false
		}
	}
	return true
}

func gsParseWriteRequest(payload []byte) ([]gsWrite, []gsDeferment, *gsError) {
	var writes []gsWrite
	var defs []gsDeferment
	var failure *gsError
	err := visitProto(payload, func(f, w, _ uint64, v []byte) error {
		if (f != 1 && f != 2) || failure != nil {
			return nil
		}
		if w != 2 {
			return errors.New("Invalid operation")
		}
		if f == 1 {
			op, oerr := gsParseWriteOperation(v)
			if oerr != nil {
				failure = oerr
				return nil
			}
			writes = append(writes, op)
		} else {
			d, derr := gsParseDeferment(v)
			if derr != nil {
				failure = derr
				return nil
			}
			defs = append(defs, d)
		}
		return nil
	})
	switch {
	case err != nil:
		return nil, nil, gsFail("3", "Invalid write")
	case failure != nil:
		return nil, nil, failure
	case len(writes) > gsMaxOps || len(defs) > gsMaxDeferments:
		return nil, nil, gsFail("8", "Too many operations")
	case len(writes) == 0 && len(defs) == 0:
		return nil, nil, gsFail("3", "Empty write")
	}
	return writes, defs, nil
}

// ------------------------------------------------------------- application

type gsChange struct {
	path string
	doc  *gsDoc // nil: deleted.
}

func gsInMask(mask []string, key string) bool {
	for _, p := range mask {
		if p == "*" && key == "*" {
			return true
		}
		field, err := gsFlatFieldPath(p)
		if err == nil && field == key {
			return true
		}
	}
	return false
}

func gsLookup(fields []gsField, key string) ([]byte, bool) {
	for _, f := range fields {
		if f.key == key {
			return f.value, true
		}
	}
	return nil, false
}

// HYPOTHESIS (inferred from the observed load, maximum, increment, store
// sequence on one presence-document field): a sequence counter per global
// value. Globals belong ONLY to the room. Loading a missing global leaves
// the field unchanged; maximum and increment on a missing field start from
// its value/zero; store saves the field.
// Each result is the field's value after the transform.
func gsApplyTransform(doc *gsDoc, ft gsTransform, globals func() map[string][]byte) ([]byte, *gsError) {
	field, pathErr := gsFlatFieldPath(ft.path)
	if pathErr != nil {
		return nil, pathErr
	}
	ft.path = field
	switch ft.kind {
	case 6:
		if v, ok := globals()[ft.global]; ok {
			doc.set(ft.path, v)
		}
	case 7:
		v, ok := doc.get(ft.path)
		if !ok {
			return nil, gsFail("3", "store_global_value on a missing field")
		}
		g := globals()
		if _, exists := g[ft.global]; !exists && len(g) >= gsMaxGlobals {
			return nil, gsFail("8", "Too many global values")
		}
		g[ft.global] = v
	case 3, 4:
		opInt, oi, of, ok := gsNumber(ft.operand)
		if !ok {
			return nil, gsFail("3", "Invalid numeric operand")
		}
		cur, has := doc.get(ft.path)
		var curInt bool
		var ci int64
		var cf float64
		if has {
			var numeric bool
			curInt, ci, cf, numeric = gsNumber(cur)
			if !numeric {
				if ft.kind == 4 {
					doc.set(ft.path, ft.operand)
					return ft.operand, nil
				}
				return nil, gsFail("3", "Increment on a nonnumeric field")
			}
		}
		switch {
		case ft.kind == 4 && !has:
			doc.set(ft.path, ft.operand)
		case ft.kind == 3 && !has:
			doc.set(ft.path, ft.operand)
		case curInt && opInt && ft.kind == 4:
			if oi > ci {
				doc.set(ft.path, gsIntValue(oi))
			}
		case curInt && opInt:
			sum := ci + oi
			if (oi > 0 && sum < ci) || (oi < 0 && sum > ci) {
				return nil, gsFail("3", "Increment overflow")
			}
			doc.set(ft.path, gsIntValue(sum))
		default:
			a, b := cf, of
			if curInt {
				a = float64(ci)
			}
			if opInt {
				b = float64(oi)
			}
			if ft.kind == 4 {
				a = math.Max(a, b)
			} else {
				a += b
			}
			doc.set(ft.path, gsDoubleValue(a))
		}
	default:
		return nil, gsFail("12", "Unobserved transform type")
	}
	if v, ok := doc.get(ft.path); ok {
		return v, nil
	}
	return gsNullValue, nil
}

func (room *gamesyncRoom) applyWrites(actor gamesyncActor, writes []gsWrite, defs []gsDeferment, now time.Time) ([][]byte, int, *gsError) {
	room.mu.Lock()
	defer room.mu.Unlock()
	results, changes, err := room.applyLocked(actor, writes, defs, now)
	return results, len(changes), err
}

// Atomic batch: validate and compute on a copy, then commit and notify.
// A failure in any operation leaves no changes.
func (room *gamesyncRoom) applyLocked(actor gamesyncActor, writes []gsWrite, defs []gsDeferment, now time.Time) ([][]byte, []gsChange, *gsError) {
	over := map[string]*gsDoc{}
	var order []string
	touch := func(p string, d *gsDoc) {
		if _, ok := over[p]; !ok {
			order = append(order, p)
		}
		over[p] = d
	}
	get := func(p string) *gsDoc {
		if d, ok := over[p]; ok {
			return d
		}
		return room.docs[p]
	}
	var globals map[string][]byte
	getGlobals := func() map[string][]byte {
		if globals == nil {
			globals = make(map[string][]byte, len(room.globals)+1)
			for k, v := range room.globals {
				globals[k] = v
			}
		}
		return globals
	}
	var results [][]byte
	for _, wr := range writes {
		if err := room.authorizeSignalWrite(actor, wr, get(wr.path)); err != nil {
			return nil, nil, err
		}
		if wr.precond {
			return nil, nil, gsFail("12", "Preconditions not observed")
		}
		switch wr.kind {
		case 1:
			if len(wr.mask) == 0 {
				return nil, nil, gsFail("12", "Unobserved update_document without mask")
			}
			wildcard := false
			normalizedMask := make([]string, len(wr.mask))
			for i, p := range wr.mask {
				switch {
				case p == "*":
					wildcard = true
					normalizedMask[i] = p
				default:
					field, err := gsFlatFieldPath(p)
					if err != nil {
						return nil, nil, err
					}
					normalizedMask[i] = field
				}
			}
			wr.mask = normalizedMask
			next := &gsDoc{created: now}
			if cur := get(wr.path); cur != nil {
				next = cur.clone()
			}
			if wildcard {
				// HYPOTHESIS: "*" replaces the entire document (AIP-134 FieldMask convention).
				// In the traces, masks on the first two writes are one character long,
				// although the documents contain two and five fields;
				// the diagnostic will establish whether the mask was "*".
				next.fields = append([]gsField(nil), wr.fields...)
			}
			for _, p := range wr.mask {
				if p == "*" {
					continue
				}
				if v, ok := gsLookup(wr.fields, p); ok {
					next.set(p, v)
				} else {
					next.remove(p)
				}
			}
			if gamesyncMaskedUpdateStoresUnmasked {
				for _, f := range wr.fields {
					if !gsInMask(wr.mask, f.key) {
						next.set(f.key, f.value)
					}
				}
			}
			next.updated = now
			if classifyGamesyncPath(wr.path).kind == gsPathUserState && resourceLeaf(wr.path) != actor.usid {
				if err := room.validateSignalDocument(actor, next); err != nil {
					return nil, nil, err
				}
				next.signalingOwner = actor.usid
			}
			if len(next.fields) > gsMaxDocFields || next.size() > gsMaxDocBytes {
				return nil, nil, gsFail("8", "Document too large")
			}
			touch(wr.path, next)
			results = append(results, protoBytes(nil, 1, nil))
		case 3:
			cur := get(wr.path)
			if cur == nil {
				return nil, nil, gsFail("5", "Transform document does not exist")
			}
			next := cur.clone()
			next.updated = now
			var values []byte
			for _, ft := range wr.transforms {
				v, err := gsApplyTransform(next, ft, getGlobals)
				if err != nil {
					return nil, nil, err
				}
				values = protoBytes(values, 1, v)
			}
			touch(wr.path, next)
			results = append(results, protoBytes(nil, 2, values))
		case 4:
			if get(wr.path) != nil {
				touch(wr.path, nil)
			}
			results = append(results, protoBytes(nil, 3, nil))
		default:
			return nil, nil, gsFail("12", "Unobserved operation")
		}
	}
	// Validate deferments before committing any changes.
	pending := len(room.deferments[actor.usid])
	for _, d := range defs {
		if !d.update {
			continue
		}
		if len(d.ops) == 0 {
			return nil, nil, gsFail("12", "Deferment without writes not observed")
		}
		for _, op := range d.ops {
			if op.kind != 4 {
				return nil, nil, gsFail("12", "Non-delete deferment not observed")
			}
			if op.precond {
				return nil, nil, gsFail("12", "Preconditions not observed")
			}
			if err := room.authorizeSignalWrite(actor, op, get(op.path)); err != nil {
				return nil, nil, err
			}
		}
		if _, exists := room.deferments[actor.usid][d.name]; !exists {
			pending++
		}
	}
	if pending > gsMaxDefermentsP {
		return nil, nil, gsFail("8", "Too many deferments")
	}
	count := len(room.docs)
	for p, d := range over {
		_, existed := room.docs[p]
		if d != nil && !existed {
			count++
		}
		if d == nil && existed {
			count--
		}
	}
	if count > gsMaxRoomDocs {
		return nil, nil, gsFail("8", "Too many documents in room")
	}
	if len(globals) > gsMaxGlobals {
		return nil, nil, gsFail("8", "Too many global values")
	}

	var changes []gsChange
	for _, p := range order {
		d := over[p]
		_, existed := room.docs[p]
		if d == nil {
			if existed {
				delete(room.docs, p)
				changes = append(changes, gsChange{p, nil})
			}
			continue
		}
		room.docs[p] = d
		changes = append(changes, gsChange{p, d})
	}
	if globals != nil {
		room.globals = globals
	}
	for _, d := range defs {
		if actor.usid == "" {
			break
		}
		if !d.update {
			delete(room.deferments[actor.usid], d.name)
			continue
		}
		if room.deferments[actor.usid] == nil {
			room.deferments[actor.usid] = make(map[string][]gsWrite)
		}
		room.deferments[actor.usid][d.name] = d.ops
	}
	room.notifyLocked(changes)
	return results, changes, nil
}

// ------------------------------------------------------- watches and delivery

func gsDocumentChange(id string, typ uint64, path string, d *gsDoc) []byte {
	change := protoBytes(nil, 1, []byte(id))
	change = protoVarint(change, 2, typ)
	if d != nil {
		change = protoBytes(change, 3, encodeGamesyncDocument(path, d))
	} else {
		change = protoBytes(change, 3, protoBytes(nil, 1, []byte(path)))
	}
	return protoBytes(nil, 2, change)
}

func (room *gamesyncRoom) notifyLocked(changes []gsChange) {
	if len(changes) == 0 {
		return
	}
	channels := make([]*gamesyncChannel, 0, len(room.channels))
	for ch := range room.channels {
		channels = append(channels, ch)
	}
	for _, c := range changes {
		for _, ch := range channels {
			names := make([]string, 0, len(ch.watches))
			for name := range ch.watches {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				wt := ch.watches[name]
				if !wt.matches(c.path) {
					continue
				}
				typ := uint64(2) // UPDATED
				if c.doc == nil {
					typ = 3 // DELETED
				}
				ch.q.push(gsDocumentChange(wt.id, typ, c.path, c.doc))
			}
		}
	}
}

// Initial snapshot and watch registration under the same lock: no change
// can occur between the read and subscription.
func (room *gamesyncRoom) installWatch(ch *gamesyncChannel, actor gamesyncActor, target gamesyncTarget, logger *log.Logger) (frames [][]byte) {
	room.mu.Lock()
	defer func() {
		// Enqueue before releasing the room: no write can deliver a live change
		// ahead of the UPDATED/EXIST/LISTED snapshot.
		ch.q.push(frames...)
		room.mu.Unlock()
	}()
	fail := func(err *gsError) [][]byte {
		return gamesyncTargetFailure(target.name, err.codeNumber(), err.message)
	}
	for _, path := range target.paths {
		gp := classifyGamesyncPath(path)
		id := resourceLeaf(path)
		logger.Printf("Gamesync requested document: path_schema=%q id_matches_usid=%t id_matches_gsid=%t id_matches_uid=%t", documentPathShape(path), id == actor.usid, id == actor.gsid, id == actor.owner)
		switch target.kind {
		case 2:
			if gsIsCollection(gp.kind) {
				return fail(gsFail("3", "Collection path in a document target"))
			}
			if err := room.authorizeRead(actor, gp); err != nil {
				return fail(err)
			}
		case 3:
			if !gsIsCollection(gp.kind) {
				if gp.kind == gsPathInvalid {
					return fail(gsFail("3", "Invalid collection path"))
				}
				return fail(gsFail("12", "Gamesync collection not implemented"))
			}
		default:
			return fail(gsFail("12", "Target type not implemented"))
		}
	}
	frames = [][]byte{gamesyncTargetChange(target.name, 1)} // UPDATED: target installed.
	id := resourceLeaf(target.name)
	wt := &gsWatch{name: target.name, id: id, kind: target.kind, paths: map[string]bool{}}
	sent := 0
	if target.kind == 2 {
		for _, path := range target.paths {
			wt.paths[path] = true
			if d := room.docs[path]; d != nil {
				frames = append(frames, gsDocumentChange(id, 1, path, d)) // EXIST
				sent++
			}
		}
	} else {
		wt.collection = target.paths[0]
		var children []string
		for path := range room.docs {
			if wt.matches(path) {
				children = append(children, path)
			}
		}
		sort.Strings(children)
		for _, path := range children {
			frames = append(frames, gsDocumentChange(id, 1, path, room.docs[path]))
			sent++
		}
	}
	frames = append(frames, gamesyncTargetChange(target.name, 2)) // LISTED
	ch.watches[target.name] = wt
	logger.Printf("Gamesync watch installed: type=%d references=%d initial_documents=%d sequence=UPDATED-EXIST-LISTED target_id=last-segment", target.kind, len(target.paths), sent)
	return frames
}

func (room *gamesyncRoom) removeWatch(ch *gamesyncChannel, name string, logger *log.Logger) (frames [][]byte) {
	room.mu.Lock()
	defer func() {
		ch.q.push(frames...)
		room.mu.Unlock()
	}()
	_, known := ch.watches[name]
	logger.Printf("Gamesync DeleteTarget: name_schema=%q installed_on_channel=%t", documentPathShape(name), known)
	if !known {
		return gamesyncTargetFailure(name, 5, "Target not installed on this channel")
	}
	delete(ch.watches, name)
	logger.Print("Gamesync DeleteTarget: removed=true stream_open=true")
	return [][]byte{gamesyncTargetChange(name, 3)}
}

func (room *gamesyncRoom) readDocument(actor gamesyncActor, path string) ([]byte, *gsError) {
	room.mu.Lock()
	defer room.mu.Unlock()
	gp := classifyGamesyncPath(path)
	if gp.kind == gsPathInvalid || gsIsCollection(gp.kind) {
		return nil, gsFail("12", "Gamesync document not implemented")
	}
	if err := room.authorizeRead(actor, gp); err != nil {
		return nil, err
	}
	d := room.docs[path]
	if d == nil {
		return nil, gsFail("5", "Document does not exist")
	}
	return encodeGamesyncDocument(path, d), nil
}
