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

func TestDeleteTargetKeepsChannelUsable(t *testing.T) {
	s := newTicketStore()
	s.issuedGamesync[sha256.Sum256([]byte("access"))] = issuedMatch{ticket: pendingTicket{owner: "owner", userName: "userSessions/player", createdAt: time.Now()}, expires: time.Now().Add(time.Hour)}
	remove := protoBytes(nil, 1, []byte("userSessions/current"))
	remove = protoBytes(remove, 4, protoBytes(nil, 1, []byte("userSessions/current/targets/1")))
	echo := protoBytes(nil, 1, []byte("userSessions/current"))
	echo = protoBytes(echo, 2, []byte("alive"))
	body := streamTestFrame(testUserDocumentWatch("docs/__us/player"))
	body = append(body, streamTestFrame(remove)...)
	body = append(body, streamTestFrame(echo)...)
	// Repeated deletion reports NOT_FOUND without closing the channel or recreating the target.
	body = append(body, streamTestFrame(remove)...)
	body = append(body, streamTestFrame(echo)...)
	r := httptest.NewRequest("POST", gamesyncKeepUserSessionPath, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/grpc")
	r.Header.Set("Authorization", "Bearer access")
	w := httptest.NewRecorder()
	s.keepGamesyncSession(log.New(io.Discard, "", 0))(w, r)
	for i := 0; i < 3; i++ {
		if _, err := readGRPCStreamFrame(w.Body); err != nil {
			t.Fatal(err)
		}
	}
	p, err := readGRPCStreamFrame(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	change := decodeTestMessage(t, decodeTestMessage(t, p)[3])
	if string(change[1]) != "1" || !bytes.Equal(change[2], []byte{3}) {
		t.Fatal("Incorrect deletion")
	}
	p, err = readGRPCStreamFrame(w.Body)
	if err != nil || !bytes.Equal(p, protoBytes(nil, 1, []byte("alive"))) {
		t.Fatal("Deletion closed the channel")
	}
	p, err = readGRPCStreamFrame(w.Body)
	if err != nil {
		t.Fatal(err)
	}
	change = decodeTestMessage(t, decodeTestMessage(t, p)[3])
	if !bytes.Equal(change[2], []byte{4}) || !bytes.Equal(decodeTestMessage(t, change[3])[1], []byte{5}) {
		t.Fatal("Deleted target must not exist")
	}
	p, err = readGRPCStreamFrame(w.Body)
	if err != nil || !bytes.Equal(p, protoBytes(nil, 1, []byte("alive"))) {
		t.Fatal("Target failure closed the channel")
	}
}

func TestWriteDiagnosticDoesNotPretendSuccessOrRevealValues(t *testing.T) {
	s := newTicketStore()
	s.issuedGamesync[sha256.Sum256([]byte("SECRET_ACCESS"))] = issuedMatch{ticket: pendingTicket{userName: "userSessions/SECRET_ID", sessionName: "gameSessions/SECRET_ROOM"}, expires: time.Now().Add(time.Hour)}
	doc := protoBytes(nil, 1, []byte("docs/__us/SECRET_ID"))
	entry := protoBytes(nil, 1, []byte("SECRET_KEY"))
	entry = protoBytes(entry, 2, protoBytes(nil, 7, []byte("SECRET_VALUE")))
	doc = protoBytes(doc, 2, protoBytes(nil, 1, entry))
	merge := protoBytes(nil, 1, doc)
	payload := protoBytes(nil, 1, protoBytes(nil, 2, merge))
	for _, token := range []string{"SECRET_ACCESS", "invalid"} {
		r := httptest.NewRequest("POST", gamesyncWriteDocumentsPath, bytes.NewReader(streamTestFrame(payload)))
		r.Header.Set("Content-Type", "application/grpc")
		r.Header.Set("Authorization", "Bearer "+token)
		var logs bytes.Buffer
		w := httptest.NewRecorder()
		s.inspectGamesyncWrites(log.New(&logs, "", 0))(w, r)
		res := w.Result()
		res.Body.Close()
		status := "12"
		if token == "invalid" {
			status = "16"
		}
		if res.Trailer.Get("Grpc-Status") != status {
			t.Fatal("Incorrect status")
		}
		if strings.Contains(logs.String(), "SECRET") {
			t.Fatal("Log reveals private content")
		}
		if token == "SECRET_ACCESS" && (!strings.Contains(logs.String(), "operation=2") || !strings.Contains(logs.String(), "fields=1") || !strings.Contains(logs.String(), "id_matches_usid=true")) {
			t.Fatal("Incomplete diagnostic")
		}
	}
}

func TestTransformAndDefermentDiagnosticHidesContents(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(&output, "", 0)
	transform := protoBytes(nil, 1, []byte("SECRET_FIELD.nested"))
	transform = protoBytes(transform, 3, protoVarint(nil, 3, 42))
	if err := inspectGamesyncTransform(transform, logger); err != nil {
		t.Fatal(err)
	}
	deferment := protoBytes(nil, 1, []byte("userSessions/SECRET_ID/deferments/SECRET_NAME"))
	deletion := protoBytes(nil, 1, []byte("docs/__pgn/All/__pus/SECRET_ID"))
	deferment = protoBytes(deferment, 2, protoBytes(nil, 4, deletion))
	if err := inspectGamesyncDeferment(protoBytes(nil, 1, protoBytes(nil, 1, deferment)), logger); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "SECRET") || !strings.Contains(output.String(), "type=3 value_type=3") || !strings.Contains(output.String(), "operation=4") || !strings.Contains(output.String(), "writes=1") {
		t.Fatal("Diagnostic contains private data or is incomplete")
	}
	if inspectGamesyncTransform(protoBytes(nil, 1, []byte("field")), logger) == nil {
		t.Fatal("Accepted incomplete transform")
	}
}
