package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- request builders (test structure only, no real client traffic) ----

func fld(key string, value []byte) gsField { return gsField{key: key, value: value} }

func tvStr(s string) []byte { return protoBytes(nil, 7, []byte(s)) }
func tvInt(i int64) []byte  { return gsIntValue(i) }

func TestWatchSnapshotIsQueuedBeforeLiveWrites(t *testing.T) {
	ticket := pendingTicket{owner: "owner", userName: "userSessions/player", sessionName: "gameSessions/room", createdAt: time.Now()}
	store := newTicketStore()
	room := store.gamesyncRoom(ticket)
	actor := gamesyncActorFor(ticket)
	ch := room.openChannel(actor)
	defer room.closeChannel(ch, log.New(io.Discard, "", 0))
	path := "docs/__pgn/All/__pus/player"
	room.installWatch(ch, actor, gamesyncTarget{name: "userSessions/current/targets/1", kind: 2, paths: []string{path}}, log.New(io.Discard, "", 0))
	_, _, err := room.applyWrites(actor, []gsWrite{{kind: 1, path: path, mask: []string{"*"}, fields: []gsField{fld("uid", tvStr("owner"))}}}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	frames, open := ch.q.next()
	if !open || len(frames) != 3 {
		t.Fatalf("Expected two initial messages and a live change; received %d", len(frames))
	}
	first := decodeTestMessage(t, decodeTestMessage(t, frames[0])[3])
	second := decodeTestMessage(t, decodeTestMessage(t, frames[1])[3])
	third := decodeTestMessage(t, decodeTestMessage(t, frames[2])[2])
	if !bytes.Equal(first[2], []byte{1}) || !bytes.Equal(second[2], []byte{2}) || !bytes.Equal(third[2], []byte{2}) {
		t.Fatal("Live update arrived before UPDATED/LISTED")
	}
}

func testDocBytes(path string, fields ...gsField) []byte {
	var m []byte
	for _, f := range fields {
		entry := protoBytes(nil, 1, []byte(f.key))
		entry = protoBytes(entry, 2, f.value)
		m = protoBytes(m, 1, entry)
	}
	return protoBytes(protoBytes(nil, 1, []byte(path)), 2, m)
}

func testOpUpdate(path string, mask []string, fields ...gsField) []byte {
	req := protoBytes(nil, 1, testDocBytes(path, fields...))
	if len(mask) > 0 {
		var m []byte
		for _, p := range mask {
			m = protoBytes(m, 1, []byte(p))
		}
		req = protoBytes(req, 2, m)
	}
	return protoBytes(nil, 1, req)
}

func testFT(path string, kind uint64, operand []byte, global string) []byte {
	ft := protoBytes(nil, 1, []byte(path))
	switch kind {
	case 3, 4, 5:
		ft = protoBytes(ft, kind, operand)
	case 6, 7, 8:
		ft = protoBytes(ft, kind, []byte(global))
	case 2:
		ft = protoVarint(ft, 2, 1)
	}
	return ft
}

func testOpTransform(path string, fts ...[]byte) []byte {
	req := protoBytes(nil, 1, []byte(path))
	for _, ft := range fts {
		req = protoBytes(req, 2, ft)
	}
	return protoBytes(nil, 3, req)
}

func testOpDelete(path string) []byte { return protoBytes(nil, 4, protoBytes(nil, 1, []byte(path))) }

func testDeferUpdate(name string, ops ...[]byte) []byte {
	d := protoBytes(nil, 1, []byte(name))
	for _, op := range ops {
		d = protoBytes(d, 2, op)
	}
	return protoBytes(nil, 1, protoBytes(nil, 1, d))
}

func testDeferDelete(name string) []byte { return protoBytes(nil, 2, protoBytes(nil, 1, []byte(name))) }

func testWriteRequest(ops [][]byte, defs ...[]byte) []byte {
	var req []byte
	for _, op := range ops {
		req = protoBytes(req, 1, op)
	}
	for _, d := range defs {
		req = protoBytes(req, 2, d)
	}
	return req
}

func testWatchRequest(id string, collection bool, path string) []byte {
	target := protoBytes(nil, 1, []byte("userSessions/current/targets/"+id))
	if collection {
		target = protoBytes(target, 3, protoBytes(nil, 1, []byte(path)))
	} else {
		target = protoBytes(target, 2, protoBytes(nil, 1, []byte(path)))
	}
	return protoBytes(protoBytes(nil, 1, []byte("userSessions/current")), 3, protoBytes(nil, 1, target))
}

func addTestPlayer(s *ticketStore, access, room, player string) pendingTicket {
	ticket := pendingTicket{
		owner: "owner-" + player, userName: "tenants/" + labTenant + "/gameSessions/" + room + "/userSessions/" + player,
		sessionName: "tenants/" + labTenant + "/gameSessions/" + room, createdAt: time.Now().UTC(),
	}
	s.issuedGamesync[sha256.Sum256([]byte(access))] = issuedMatch{ticket: ticket, expires: time.Now().Add(time.Hour)}
	return ticket
}

func presencePath(id string) string { return "docs/__pgn/All/__pus/" + id }

const presenceCollection = "docs/__pgn/All/__pus"

func callWrite(t *testing.T, s *ticketStore, access string, payload []byte, logger *log.Logger) (string, []byte) {
	t.Helper()
	r := httptest.NewRequest("POST", gamesyncWriteDocumentsPath, bytes.NewReader(streamTestFrame(payload)))
	r.Header.Set("Content-Type", "application/grpc")
	r.Header.Set("Authorization", "Bearer "+access)
	w := httptest.NewRecorder()
	s.inspectGamesyncWrites(logger)(w, r)
	res := w.Result()
	res.Body.Close()
	var body []byte
	if res.Trailer.Get("Grpc-Status") == "0" {
		var err error
		if body, err = readGRPCStreamFrame(w.Body); err != nil {
			t.Fatal(err)
		}
	}
	return res.Trailer.Get("Grpc-Status"), body
}

func quiet() *log.Logger { return log.New(io.Discard, "", 0) }

func callGet(t *testing.T, s *ticketStore, access, path string) (string, map[string][]byte) {
	t.Helper()
	r := httptest.NewRequest("POST", gamesyncGetDocumentPath, bytes.NewReader(streamTestFrame(protoBytes(nil, 1, []byte(path)))))
	r.Header.Set("Content-Type", "application/grpc")
	r.Header.Set("Authorization", "Bearer "+access)
	w := httptest.NewRecorder()
	s.getGamesyncDocument(quiet())(w, r)
	res := w.Result()
	res.Body.Close()
	if res.Trailer.Get("Grpc-Status") != "0" {
		return res.Trailer.Get("Grpc-Status"), nil
	}
	doc, err := readGRPCStreamFrame(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	return "0", docFields(t, doc)
}

func docFields(t *testing.T, doc []byte) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	_ = visitProto(decodeTestMessage(t, doc)[2], func(f, w, _ uint64, v []byte) error {
		if f == 1 && w == 2 {
			entry := decodeTestMessage(t, v)
			out[string(entry[1])] = entry[2]
		}
		return nil
	})
	return out
}

func intOf(t *testing.T, value []byte) int64 {
	t.Helper()
	var got int64 = -1 << 62
	_ = visitProto(value, func(f, w, n uint64, _ []byte) error {
		if f == 3 && w == 0 {
			got = int64(n)
		}
		return nil
	})
	if got == -1<<62 {
		t.Fatal("Value is not an integer")
	}
	return got
}

func transformValues(t *testing.T, response []byte) []int64 {
	t.Helper()
	var out []int64
	_ = visitProto(response, func(f, w, _ uint64, result []byte) error {
		if f != 1 || w != 2 {
			return nil
		}
		return visitProto(result, func(rf, rw, _ uint64, tr []byte) error {
			if rf == 2 && rw == 2 {
				return visitProto(tr, func(vf, vw, _ uint64, v []byte) error {
					if vf == 1 && vw == 2 {
						out = append(out, intOf(t, v))
					}
					return nil
				})
			}
			return nil
		})
	})
	return out
}

// ----------------------------------------------------------- writes

func TestMaskedUpdateStoresOnlyMaskedFieldsAndReadsBack(t *testing.T) {
	s := newTicketStore()
	addTestPlayer(s, "acc", "room", "p1")
	path := presencePath("p1")
	status, _ := callWrite(t, s, "acc", testWriteRequest([][]byte{testOpUpdate(path, []string{"a"}, fld("a", tvStr("one")), fld("b", tvInt(2)))}), quiet())
	if status != "0" {
		t.Fatalf("Status %s", status)
	}
	status, fields := callGet(t, s, "acc", path)
	if status != "0" || len(fields) != 1 || string(decodeTestMessage(t, fields["a"])[7]) != "one" {
		t.Fatal("Only the masked field must be stored")
	}
	// A masked field absent from the document is deleted; another is added.
	callWrite(t, s, "acc", testWriteRequest([][]byte{testOpUpdate(path, []string{"c"}, fld("c", tvInt(3)))}), quiet())
	if _, fields = callGet(t, s, "acc", path); len(fields) != 2 || intOf(t, fields["c"]) != 3 {
		t.Fatal("Field c must exist")
	}
	callWrite(t, s, "acc", testWriteRequest([][]byte{testOpUpdate(path, []string{"c"})}), quiet())
	if _, fields = callGet(t, s, "acc", path); len(fields) != 1 || fields["c"] != nil {
		t.Fatal("An absent masked field must be deleted")
	}
	gamesyncMaskedUpdateStoresUnmasked = true
	defer func() { gamesyncMaskedUpdateStoresUnmasked = false }()
	callWrite(t, s, "acc", testWriteRequest([][]byte{testOpUpdate(path, []string{"a"}, fld("a", tvStr("one")), fld("b", tvInt(2)))}), quiet())
	if _, fields = callGet(t, s, "acc", path); intOf(t, fields["b"]) != 2 {
		t.Fatal("The unmasked-field merge variant does not work")
	}
}

func TestWildcardMaskReplacesTheWholeDocument(t *testing.T) {
	s := newTicketStore()
	addTestPlayer(s, "acc", "room", "p1")
	path := "docs/Coleccion/p1"
	callWrite(t, s, "acc", testWriteRequest([][]byte{testOpUpdate(path, []string{"a"}, fld("a", tvInt(1)), fld("b", tvInt(2)))}), quiet())
	status, _ := callWrite(t, s, "acc", testWriteRequest([][]byte{testOpUpdate(path, []string{"*"}, fld("c", tvInt(3)), fld("d", tvInt(4)))}), quiet())
	_, fields := callGet(t, s, "acc", path)
	if status != "0" || len(fields) != 2 || intOf(t, fields["c"]) != 3 || intOf(t, fields["d"]) != 4 || fields["a"] != nil {
		t.Fatal("\"*\" must replace the document fields")
	}
}

func TestUnknownOperationsAreRejectedAtomicallyWithoutTrace(t *testing.T) {
	s := newTicketStore()
	addTestPlayer(s, "acc", "room", "p1")
	addTestPlayer(s, "other", "room", "p2")
	own, foreign := presencePath("p1"), presencePath("p2")
	valid := testOpUpdate(own, []string{"a"}, fld("a", tvInt(1)))
	for name, tc := range map[string]struct {
		payload []byte
		status  string
	}{
		"Unobserved transform":  {testWriteRequest([][]byte{valid, testOpTransform(own, testFT("a", 8, nil, "g"))}), "12"},
		"set_server_value":      {testWriteRequest([][]byte{valid, testOpTransform(own, testFT("a", 2, nil, ""))}), "12"},
		"minimum":               {testWriteRequest([][]byte{valid, testOpTransform(own, testFT("a", 5, tvInt(1), ""))}), "12"},
		"Nested field":          {testWriteRequest([][]byte{valid, testOpTransform(own, testFT("a.b", 3, tvInt(1), ""))}), "12"},
		"merge":                 {testWriteRequest([][]byte{valid, protoBytes(nil, 2, protoBytes(nil, 1, testDocBytes(own)))}), "12"},
		"Without mask":          {testWriteRequest([][]byte{valid, testOpUpdate(own, nil, fld("a", tvInt(1)))}), "12"},
		"Nested mask":           {testWriteRequest([][]byte{valid, testOpUpdate(own, []string{"a.b"}, fld("a", tvInt(1)))}), "12"},
		"System document":       {testWriteRequest([][]byte{valid, testOpUpdate("docs/__us/p1", []string{"a"}, fld("a", tvInt(1)))}), "12"},
		"Unauthorized write":    {testWriteRequest([][]byte{valid, testOpUpdate(foreign, []string{"a"}, fld("a", tvInt(1)))}), "7"},
		"Unauthorized deletion": {testWriteRequest([][]byte{valid, testOpDelete(foreign)}), "7"},
		"Non-delete deferment":  {testWriteRequest([][]byte{valid}, testDeferUpdate("userSessions/current/deferments/x", valid)), "12"},
		"Deferment on another participant's document": {testWriteRequest([][]byte{valid}, testDeferUpdate("userSessions/current/deferments/x", testOpDelete(foreign))), "7"},
		"Transform without document":                  {testWriteRequest([][]byte{testOpTransform(presencePath("p1"), testFT("a", 3, tvInt(1), ""))}), "5"},
		"Value is not numeric":                        {testWriteRequest([][]byte{valid, testOpTransform(own, testFT("a", 3, tvStr("x"), ""))}), "3"},
	} {
		if got, _ := callWrite(t, s, "acc", tc.payload, quiet()); got != tc.status {
			t.Fatalf("%s: status %s, expected %s", name, got, tc.status)
		}
		if status, _ := callGet(t, s, "acc", own); status != "5" {
			t.Fatalf("%s: rejected batch left changes", name)
		}
	}
	// Unobserved precondition: explicit rejection rather than fabricated success.
	update := protoBytes(nil, 1, testDocBytes(own, fld("a", tvInt(1))))
	update = protoBytes(update, 2, protoBytes(nil, 1, []byte("a")))
	update = protoBytes(update, 3, protoVarint(nil, 1, 1))
	if got, _ := callWrite(t, s, "acc", testWriteRequest([][]byte{protoBytes(nil, 1, update)}), quiet()); got != "12" {
		t.Fatalf("Precondition: status %s", got)
	}
	if got, _ := callWrite(t, s, "acc", testWriteRequest(nil), quiet()); got != "3" {
		t.Fatalf("Empty request: status %s", got)
	}
	if got, _ := callWrite(t, s, "forged", testWriteRequest([][]byte{valid}), quiet()); got != "16" {
		t.Fatalf("Unknown token: status %s", got)
	}
}

func TestTransformGlobalValuesAreScopedToTheRoom(t *testing.T) {
	s := newTicketStore()
	addTestPlayer(s, "a1", "room1", "a1")
	addTestPlayer(s, "a2", "room1", "a2")
	addTestPlayer(s, "b1", "room2", "b1")
	sequence := func(access, id string, max, inc int64) []int64 {
		path := presencePath(id)
		write := testWriteRequest([][]byte{
			testOpUpdate(path, []string{"seq"}, fld("seq", tvInt(0))),
			testOpTransform(path,
				testFT("seq", 6, nil, "counter"), testFT("seq", 4, tvInt(max), ""),
				testFT("seq", 3, tvInt(inc), ""), testFT("seq", 7, nil, "counter")),
		})
		status, response := callWrite(t, s, access, write, quiet())
		if status != "0" {
			t.Fatalf("Status %s", status)
		}
		return transformValues(t, response)
	}
	equal := func(got []int64, want ...int64) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	// Loading a missing global leaves the field; max and increment raise it to 7; store saves it.
	if got := sequence("a1", "a1", 5, 2); !equal(got, 0, 5, 7, 7) {
		t.Fatalf("a1: %v", got)
	}
	// Same room: the next participant starts from the saved global (7).
	if got := sequence("a2", "a2", 5, 2); !equal(got, 7, 7, 9, 9) {
		t.Fatalf("a2: %v", got)
	}
	// Another room cannot see room1's counter.
	if got := sequence("b1", "b1", 1, 4); !equal(got, 0, 1, 5, 5) {
		t.Fatalf("b1: %v", got)
	}
	// room1's global was not contaminated by room2's value.
	if got := sequence("a1", "a1", 0, 1); !equal(got, 9, 9, 10, 10) {
		t.Fatalf("a1 again: %v", got)
	}
	_, fields := callGet(t, s, "a1", presencePath("a1"))
	if intOf(t, fields["seq"]) != 10 {
		t.Fatal("Document missing the final value")
	}
	// Overflow and floating-point values.
	over := testWriteRequest([][]byte{testOpTransform(presencePath("a1"), testFT("seq", 3, tvInt(1<<62), ""), testFT("seq", 3, tvInt(1<<62), ""), testFT("seq", 3, tvInt(1<<62), ""))})
	if got, _ := callWrite(t, s, "a1", over, quiet()); got != "3" {
		t.Fatalf("Overflow: status %s", got)
	}
	if _, fields = callGet(t, s, "a1", presencePath("a1")); intOf(t, fields["seq"]) != 10 {
		t.Fatal("Overflow modified the document")
	}
}

func TestRoomsAndParticipantsAreIsolated(t *testing.T) {
	s := newTicketStore()
	addTestPlayer(s, "a1", "room1", "a1")
	addTestPlayer(s, "a2", "room1", "a2")
	addTestPlayer(s, "b1", "room2", "b1")
	game := "docs/Coleccion/a1"
	if got, _ := callWrite(t, s, "a1", testWriteRequest([][]byte{
		testOpUpdate(presencePath("a1"), []string{"x"}, fld("x", tvInt(1))),
		testOpUpdate(game, []string{"y"}, fld("y", tvInt(2))),
	}), quiet()); got != "0" {
		t.Fatalf("Own write: %s", got)
	}
	for _, tc := range []struct{ access, path, status string }{
		{"a1", presencePath("a1"), "0"}, // Own document.
		{"a2", presencePath("a1"), "0"}, // Roster in the same room.
		{"b1", presencePath("a1"), "7"}, // Another room: unrelated participant.
		{"a2", game, "0"},               // Game data shared within the room.
		{"b1", game, "7"},
		{"a1", "docs/__us/a2", "0"}, // This room's roster.
		{"b1", "docs/__us/a1", "7"},
		{"a1", "docs/__us/a1", "0"},
		{"a1", "docs/__gs/f", "0"}, // This room's fixed document.
	} {
		if status, _ := callGet(t, s, tc.access, tc.path); status != tc.status {
			t.Fatalf("%s reads %s: %s, expected %s", tc.access, tc.path, status, tc.status)
		}
	}
	// Unauthorized writes rejected without changes.
	for _, access := range []string{"a2", "b1"} {
		if got, _ := callWrite(t, s, access, testWriteRequest([][]byte{testOpUpdate(presencePath("a1"), []string{"x"}, fld("x", tvInt(99)))}), quiet()); got != "7" {
			t.Fatalf("%s writes in a1: %s", access, got)
		}
		if got, _ := callWrite(t, s, access, testWriteRequest([][]byte{testOpDelete(presencePath("a1"))}), quiet()); got != "7" {
			t.Fatalf("%s deletes in a1: %s", access, got)
		}
	}
	if _, fields := callGet(t, s, "a1", presencePath("a1")); intOf(t, fields["x"]) != 1 {
		t.Fatal("Unauthorized access modified the document")
	}
	// Watches: the roster collection exposes only its own room.
	for _, tc := range []struct {
		access string
		docs   int
	}{{"a2", 1}, {"b1", 0}} {
		req := httptest.NewRequest("POST", gamesyncKeepUserSessionPath, bytes.NewReader(streamTestFrame(testWatchRequest("1", true, presenceCollection))))
		req.Header.Set("Content-Type", "application/grpc")
		req.Header.Set("Authorization", "Bearer "+tc.access)
		rr := httptest.NewRecorder()
		s.keepGamesyncSession(quiet())(rr, req)
		frames := 0
		for {
			if _, err := readGRPCStreamFrame(rr.Body); err != nil {
				break
			}
			frames++
		}
		if frames != 2+tc.docs {
			t.Fatalf("%s: %d frames, expected %d", tc.access, frames, 2+tc.docs)
		}
	}
	// A document watch for an unknown participant in the room fails with status 7.
	req := httptest.NewRequest("POST", gamesyncKeepUserSessionPath, bytes.NewReader(streamTestFrame(testWatchRequest("1", false, presencePath("a1")))))
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Authorization", "Bearer b1")
	rr := httptest.NewRecorder()
	s.keepGamesyncSession(quiet())(rr, req)
	first, _ := readGRPCStreamFrame(rr.Body)
	change := decodeTestMessage(t, decodeTestMessage(t, first)[3])
	if !bytes.Equal(change[2], []byte{4}) || !bytes.Equal(decodeTestMessage(t, change[3])[1], []byte{7}) {
		t.Fatal("An unauthorized document watch must fail with status 7")
	}
}

// ---------------------------------------------------- actual HTTP/2 channels

type testStream struct {
	t      *testing.T
	writer *io.PipeWriter
	frames chan []byte
}

func startGamesyncServer(t *testing.T, s *ticketStore) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(gamesyncKeepUserSessionPath, s.keepGamesyncSession(quiet()))
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func openTestStream(t *testing.T, server *httptest.Server, access string, first []byte) *testStream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	reader, writer := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, "POST", server.URL+gamesyncKeepUserSessionPath, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("Authorization", "Bearer "+access)
	go func() { _, _ = writer.Write(streamTestFrame(first)) }()
	res, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.ProtoMajor != 2 {
		t.Fatal("Test requires HTTP/2")
	}
	st := &testStream{t: t, writer: writer, frames: make(chan []byte, 64)}
	go func() {
		defer close(st.frames)
		for {
			frame, err := readGRPCStreamFrame(res.Body)
			if err != nil {
				return
			}
			st.frames <- frame
		}
	}()
	t.Cleanup(func() { writer.Close(); cancel(); res.Body.Close() })
	return st
}

func (st *testStream) send(payload []byte) {
	st.t.Helper()
	if _, err := st.writer.Write(streamTestFrame(payload)); err != nil {
		st.t.Fatal(err)
	}
}

func (st *testStream) next() []byte {
	st.t.Helper()
	select {
	case f, ok := <-st.frames:
		if !ok {
			st.t.Fatal("Channel closed")
		}
		return f
	case <-time.After(3 * time.Second):
		st.t.Fatal("Timed out waiting for a frame")
	}
	return nil
}

func (st *testStream) none(d time.Duration) {
	st.t.Helper()
	select {
	case f, ok := <-st.frames:
		if ok {
			st.t.Fatalf("Unexpected frame (%d bytes)", len(f))
		}
	case <-time.After(d):
	}
}

func (st *testStream) close() { st.writer.Close() }

// describe: "target:<id>:<type>", "doc:<id>:<type>:<path>".
func describeFrame(t *testing.T, frame []byte) string {
	t.Helper()
	msg := decodeTestMessage(t, frame)
	if inner, ok := msg[3]; ok {
		c := decodeTestMessage(t, inner)
		return "target:" + string(c[1]) + ":" + string('0'+rune(c[2][0]))
	}
	c := decodeTestMessage(t, msg[2])
	return "doc:" + string(c[1]) + ":" + string('0'+rune(c[2][0])) + ":" + string(decodeTestMessage(t, c[3])[1])
}

func TestWatchesReceivePushedChangesOnlyFromTheirOwnRoom(t *testing.T) {
	s := newTicketStore()
	addTestPlayer(s, "a1", "room1", "a1")
	addTestPlayer(s, "a2", "room1", "a2")
	addTestPlayer(s, "b1", "room2", "b1")
	server := startGamesyncServer(t, s)
	one := openTestStream(t, server, "a1", testWatchRequest("1", false, presencePath("a1")))
	one.send(testWatchRequest("2", true, presenceCollection))
	two := openTestStream(t, server, "a2", testWatchRequest("1", true, presenceCollection))
	other := openTestStream(t, server, "b1", testWatchRequest("1", true, presenceCollection))
	expect := func(st *testStream, want ...string) {
		t.Helper()
		for _, w := range want {
			if got := describeFrame(t, st.next()); got != w {
				t.Fatalf("Frame %q, expected %q", got, w)
			}
		}
	}
	// Empty initial snapshot: UPDATED and LISTED, without a fabricated EXIST.
	expect(one, "target:1:1", "target:1:2", "target:2:1", "target:2:2")
	expect(two, "target:1:1", "target:1:2")
	expect(other, "target:1:1", "target:1:2")

	path := presencePath("a1")
	if got, _ := callWrite(t, s, "a1", testWriteRequest([][]byte{testOpUpdate(path, []string{"seq"}, fld("seq", tvInt(1)))}), quiet()); got != "0" {
		t.Fatalf("Status %s", got)
	}
	expect(one, "doc:1:2:"+path, "doc:2:2:"+path) // UPDATED for the document and collection watches.
	expect(two, "doc:1:2:"+path)
	other.none(250 * time.Millisecond) // Another room: no notification.

	// Unauthorized access notifies no one.
	if got, _ := callWrite(t, s, "a2", testWriteRequest([][]byte{testOpUpdate(path, []string{"seq"}, fld("seq", tvInt(9)))}), quiet()); got != "7" {
		t.Fatalf("Status %s", got)
	}
	one.none(150 * time.Millisecond)
	two.none(50 * time.Millisecond)

	// Transform: the notified document contains the committed value.
	if got, _ := callWrite(t, s, "a1", testWriteRequest([][]byte{testOpTransform(path, testFT("seq", 3, tvInt(4), ""))}), quiet()); got != "0" {
		t.Fatalf("Status %s", got)
	}
	frame := one.next()
	doc := decodeTestMessage(t, decodeTestMessage(t, decodeTestMessage(t, frame)[2])[3])
	var seq int64
	_ = visitProto(doc[2], func(f, w, _ uint64, v []byte) error {
		if f == 1 && w == 2 {
			if e := decodeTestMessage(t, v); string(e[1]) == "seq" {
				seq = intOf(t, e[2])
			}
		}
		return nil
	})
	if seq != 5 {
		t.Fatalf("seq %d", seq)
	}
	one.next()
	two.next()

	// A new channel sees the same snapshot as notifications: EXIST with the document.
	late := openTestStream(t, server, "a2", testWatchRequest("7", false, path))
	expect(late, "target:7:1", "doc:7:1:"+path, "target:7:2")

	// Deletion: DELETED to watches that include the document.
	if got, _ := callWrite(t, s, "a1", testWriteRequest([][]byte{testOpDelete(path)}), quiet()); got != "0" {
		t.Fatalf("Status %s", got)
	}
	expect(one, "doc:1:3:"+path, "doc:2:3:"+path)
	expect(two, "doc:1:3:"+path)
	expect(late, "doc:7:3:"+path)
	other.none(150 * time.Millisecond)

	// DeleteTarget stops delivery to the channel without closing it.
	late.send(protoBytes(protoBytes(nil, 1, []byte("userSessions/current")), 4, protoBytes(nil, 1, []byte("userSessions/current/targets/7"))))
	expect(late, "target:7:3")
	if got, _ := callWrite(t, s, "a1", testWriteRequest([][]byte{testOpUpdate(path, []string{"seq"}, fld("seq", tvInt(2)))}), quiet()); got != "0" {
		t.Fatalf("Status %s", got)
	}
	expect(one, "doc:1:2:"+path, "doc:2:2:"+path)
	late.none(150 * time.Millisecond)
}

func TestDefermentsCleanUpOnlyWhenTheLastChannelCloses(t *testing.T) {
	s := newTicketStore()
	addTestPlayer(s, "a1", "room1", "a1")
	addTestPlayer(s, "a2", "room1", "a2")
	server := startGamesyncServer(t, s)
	observer := openTestStream(t, server, "a2", testWatchRequest("1", true, presenceCollection))
	first := openTestStream(t, server, "a1", testWatchRequest("1", true, presenceCollection))
	second := openTestStream(t, server, "a1", testWatchRequest("1", true, presenceCollection))
	for _, st := range []*testStream{observer, first, second} {
		st.next()
		st.next()
	}
	path := presencePath("a1")
	name := "userSessions/current/deferments/x"
	// An unauthorized or non-delete cleanup write rejects the whole batch (see previous test).
	write := testWriteRequest([][]byte{testOpUpdate(path, []string{"seq"}, fld("seq", tvInt(1)))}, testDeferUpdate(name, testOpDelete(path)))
	if got, _ := callWrite(t, s, "a1", write, quiet()); got != "0" {
		t.Fatalf("Status %s", got)
	}
	for _, st := range []*testStream{observer, first, second} {
		st.next()
	}
	first.close()
	observer.none(300 * time.Millisecond) // Another channel for the participant remains open.
	if status, _ := callGet(t, s, "a2", path); status != "0" {
		t.Fatalf("Deferment executed while a channel remained open: %s", status)
	}
	second.close()
	if got := describeFrame(t, observer.next()); got != "doc:1:3:"+path {
		t.Fatalf("Frame %q", got)
	}
	if status, _ := callGet(t, s, "a2", path); status != "5" {
		t.Fatalf("Document not cleaned up: %s", status)
	}
}

func TestDeleteDefermentCancelsCleanup(t *testing.T) {
	s := newTicketStore()
	addTestPlayer(s, "a1", "room1", "a1")
	addTestPlayer(s, "a2", "room1", "a2")
	server := startGamesyncServer(t, s)
	channel := openTestStream(t, server, "a1", testWatchRequest("1", true, presenceCollection))
	channel.next()
	channel.next()
	path := presencePath("a1")
	name := "userSessions/current/deferments/x"
	if got, _ := callWrite(t, s, "a1", testWriteRequest([][]byte{testOpUpdate(path, []string{"seq"}, fld("seq", tvInt(1)))}, testDeferUpdate(name, testOpDelete(path))), quiet()); got != "0" {
		t.Fatalf("Status %s", got)
	}
	channel.next()
	if got, _ := callWrite(t, s, "a1", testWriteRequest(nil, testDeferDelete(name)), quiet()); got != "0" {
		t.Fatalf("Status %s", got)
	}
	channel.close()
	time.Sleep(200 * time.Millisecond)
	if status, _ := callGet(t, s, "a2", path); status != "0" {
		t.Fatalf("Canceled deferment executed: %s", status)
	}
}

func TestStructuredLogsNeverContainValuesOrIdentifiers(t *testing.T) {
	s := newTicketStore()
	addTestPlayer(s, "SECRET_TOKEN", "SECRET_ROOM", "SECRET_PLAYER")
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	own := presencePath("SECRET_PLAYER")
	payload := testWriteRequest([][]byte{
		testOpUpdate(own, []string{"SECRET_FIELD", "uid"}, fld("SECRET_FIELD", tvInt(1)), fld("uid", tvStr("SECRET_VALUE"))),
		testOpTransform(own, testFT("SECRET_FIELD", 6, nil, "SECRET_GLOBAL"), testFT("SECRET_FIELD", 3, tvInt(1), "")),
	}, testDeferUpdate("userSessions/current/SECRET_NAME", testOpDelete(own)))
	callWrite(t, s, "SECRET_TOKEN", payload, logger)
	callWrite(t, s, "SECRET_TOKEN", testWriteRequest([][]byte{testOpUpdate("docs/SECRET_COLLECTION/SECRET_PLAYER", []string{"SECRET_FIELD"}, fld("SECRET_FIELD", tvStr("SECRET_VALUE")))}), logger)
	callGet(t, s, "SECRET_TOKEN", own)
	if strings.Contains(logs.String(), "SECRET") {
		t.Fatalf("Log contains private data:\n%s", logs.String())
	}
	for _, want := range []string{"parsed write: operation=1", "{field}", "parsed transform: type=6", "global={global}", "parsed deferment", "applied="} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("Missing log structure: %q", want)
		}
	}
}
