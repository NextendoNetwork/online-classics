package main

import (
	"bytes"
	"crypto/sha256"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func listTestResponse(t *testing.T, response []byte) (docs [][]byte, cursor string) {
	t.Helper()
	err := visitProto(response, func(f, w, _ uint64, v []byte) error {
		if f == 1 && w == 2 {
			docs = append(docs, append([]byte(nil), v...))
		}
		if f == 2 && w == 2 {
			cursor = string(v)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestListDocumentsUsesOwnStoreAndPagination(t *testing.T) {
	s := newTicketStore()
	ticket := addTestPlayer(s, "a", "room-a", "a")
	addTestPlayer(s, "b", "room-b", "b")
	for _, collection := range []string{"items", "__opaque"} {
		if status, _ := callWrite(t, s, "a", testWriteRequest([][]byte{testOpUpdate("docs/"+collection+"/a", []string{"*"}, fld("public", tvInt(12)), fld("private", tvStr("PRIVATE_VALUE")))}), quiet()); status != "0" {
			t.Fatal(status)
		}
	}
	room := s.gamesyncRoom(ticket)
	actor := gamesyncActorFor(ticket)
	q := gsListRequest{parent: "docs/items", size: 1, mask: []string{"public"}}
	response, count, err := room.listDocuments(actor, q)
	if err != nil || count != 1 {
		t.Fatal(count, err)
	}
	docs, cursor := listTestResponse(t, response)
	if len(docs) != 1 || cursor != "" || intOf(t, docFields(t, docs[0])["public"]) != 12 || bytes.Contains(response, []byte("PRIVATE_VALUE")) {
		t.Fatal("Actual data not listed/masked")
	}
	q = gsListRequest{parent: "docs/__opaque", size: 1}
	response, _, err = room.listDocuments(actor, q)
	if err != nil {
		t.Fatal(err)
	}
	docs, _ = listTestResponse(t, response)
	if len(docs) != 1 {
		t.Fatal("Opaque collection omitted")
	}
	other := s.gamesyncRoom(s.issuedGamesync[sha256.Sum256([]byte("b"))].ticket)
	response, count, err = other.listDocuments(gamesyncActor{usid: "b", ready: true}, q)
	if err != nil || count != 0 || len(response) != 0 {
		t.Fatal("Rooms were mixed")
	}
	// The presence collection accepts members of this room, with a cursor.
	addTestPlayer(s, "a2", "room-a", "a2")
	s.gamesyncRoom(s.issuedGamesync[sha256.Sum256([]byte("a2"))].ticket)
	for _, player := range []string{"a", "a2"} {
		if status, _ := callWrite(t, s, player, testWriteRequest([][]byte{testOpUpdate(presencePath(player), []string{"*"}, fld("uid", tvStr(player)))}), quiet()); status != "0" {
			t.Fatal(status)
		}
	}
	q = gsListRequest{parent: presenceCollection, size: 1}
	response, _, err = room.listDocuments(actor, q)
	if err != nil {
		t.Fatal(err)
	}
	docs, cursor = listTestResponse(t, response)
	if len(docs) != 1 || cursor == "" || string(decodeTestMessage(t, docs[0])[1]) != presencePath("a") {
		t.Fatal("Incorrect first page")
	}
	q.cursor = cursor
	response, _, err = room.listDocuments(actor, q)
	if err != nil {
		t.Fatal(err)
	}
	docs, cursor = listTestResponse(t, response)
	if len(docs) != 1 || cursor != "" || string(decodeTestMessage(t, docs[0])[1]) != presencePath("a2") {
		t.Fatal("Incorrect second page")
	}
	for _, wrong := range []struct {
		room  *gamesyncRoom
		actor gamesyncActor
		q     gsListRequest
	}{
		{other, actor, q}, {room, gamesyncActor{usid: "a2", ready: true}, q}, {room, actor, gsListRequest{parent: "docs/items", size: 1, cursor: q.cursor}},
		{room, actor, gsListRequest{parent: presenceCollection, size: 1, cursor: q.cursor, mask: []string{"uid"}}},
	} {
		if _, _, err := wrong.room.listDocuments(wrong.actor, wrong.q); err == nil || err.code != "3" {
			t.Fatal("Accepted unauthorized cursor")
		}
	}
}

func TestListRPCAuthPrivacyAndRequestValidation(t *testing.T) {
	s := newTicketStore()
	ticket := addTestPlayer(s, "PRIVATE_TOKEN", "room", "player")
	s.gamesyncRoom(ticket)
	for _, tc := range []struct {
		parent, access, tenant, expected string
		extra                            []byte
	}{
		{"docs/__gs", "PRIVATE_TOKEN", "", "0", nil},
		{"docs/items", "PRIVATE_TOKEN", labTenant, "0", nil},
		{"docs/__gs", "forged", "", "16", nil},
		{"docs/__gs", "PRIVATE_TOKEN", "foreign", "3", nil},
		{"docs/../__gs", "PRIVATE_TOKEN", "", "3", nil},
		{"docs/__gs", "PRIVATE_TOKEN", "", "3", protoVarint(nil, 2, ^uint64(0))},
		{"docs/__gs", "PRIVATE_TOKEN", "", "12", protoBytes(nil, 4, protoBytes(nil, 1, []byte("nested.key")))},
		{"docs/__gs", "PRIVATE_TOKEN", "", "12", protoVarint(nil, 5, 1)},
	} {
		var logs bytes.Buffer
		payload := append(protoBytes(nil, 1, []byte(tc.parent)), tc.extra...)
		r := httptest.NewRequest("POST", gamesyncListDocumentsPath, bytes.NewReader(streamTestFrame(payload)))
		r.Header.Set("Content-Type", "application/grpc")
		r.Header.Set("Authorization", "Bearer "+tc.access)
		r.Header.Set("npln-tenant-id", tc.tenant)
		w := httptest.NewRecorder()
		s.listGamesyncDocuments(log.New(&logs, "", 0))(w, r)
		if got := w.Result().Trailer.Get("Grpc-Status"); got != tc.expected {
			t.Fatalf("%s: %s, expected %s", tc.parent, got, tc.expected)
		}
		if strings.Contains(logs.String(), "PRIVATE_TOKEN") || strings.Contains(logs.String(), ticket.owner) {
			t.Fatal("Log contains private data")
		}
	}
	entry := s.issuedGamesync[sha256.Sum256([]byte("PRIVATE_TOKEN"))]
	entry.expires = time.Now().Add(-time.Hour)
	s.issuedGamesync[sha256.Sum256([]byte("PRIVATE_TOKEN"))] = entry
	r := httptest.NewRequest("POST", gamesyncListDocumentsPath, bytes.NewReader(streamTestFrame(protoBytes(nil, 1, []byte("docs/__gs")))))
	r.Header.Set("Content-Type", "application/grpc")
	r.Header.Set("Authorization", "Bearer PRIVATE_TOKEN")
	w := httptest.NewRecorder()
	s.listGamesyncDocuments(quiet())(w, r)
	if w.Result().Trailer.Get("Grpc-Status") != "16" {
		t.Fatal("Accepted expired token")
	}
	// Also verify mux registration without fabricating authentication.
	r = httptest.NewRequest("POST", gamesyncListDocumentsPath, bytes.NewReader(streamTestFrame(protoBytes(nil, 1, []byte("docs/__gs")))))
	r.Header.Set("Content-Type", "application/grpc")
	w = httptest.NewRecorder()
	handler(log.New(io.Discard, "", 0)).ServeHTTP(w, r)
	if w.Result().Trailer.Get("Grpc-Status") != "16" {
		t.Fatal("ListDocuments handler not registered")
	}
}
