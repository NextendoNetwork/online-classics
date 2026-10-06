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

func testUserDocumentWatch(name string) []byte {
	target := protoBytes(nil, 1, []byte("userSessions/current/targets/1"))
	target = protoBytes(target, 2, protoBytes(nil, 1, []byte(name)))
	request := protoBytes(nil, 1, []byte("userSessions/current"))
	return protoBytes(request, 3, protoBytes(nil, 1, target))
}

func decodeTestMessage(t *testing.T, payload []byte) map[uint64][]byte {
	t.Helper()
	fields := map[uint64][]byte{}
	if err := visitProto(payload, func(f, w, n uint64, v []byte) error {
		if w == 2 {
			fields[f] = v
		} else if w == 0 {
			fields[f] = []byte{byte(n)}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return fields
}

func TestCurrentTargetIsBoundToAccessTokenAndPublishesOwnDocument(t *testing.T) {
	store := newTicketStore()
	created := time.Unix(1790956000, 123456789).UTC()
	tickets := []pendingTicket{
		{owner: "private-owner-a", userName: "tenants/" + labTenant + "/gameSessions/room-a/userSessions/player-a", sessionName: "tenants/" + labTenant + "/gameSessions/room-a", createdAt: created, user: protoBytes(nil, 4, []byte("team-a"))},
		{owner: "private-owner-b", userName: "tenants/" + labTenant + "/gameSessions/room-b/userSessions/player-b", sessionName: "tenants/" + labTenant + "/gameSessions/room-b", createdAt: created},
	}
	for i, ticket := range tickets {
		access := "private-access-" + ticket.owner
		store.issuedGamesync[sha256.Sum256([]byte(access))] = issuedMatch{ticket: ticket, expires: time.Now().Add(time.Hour)}
		for _, own := range []bool{true, false} {
			id := resourceLeaf(ticket.userName)
			if !own {
				id = resourceLeaf(tickets[1-i].userName)
			}
			path := "docs/__us/" + id
			req := httptest.NewRequest("POST", gamesyncKeepUserSessionPath, bytes.NewReader(streamTestFrame(testUserDocumentWatch(path))))
			req.Header.Set("Content-Type", "application/grpc")
			req.Header.Set("Authorization", "Bearer "+access)
			var logs bytes.Buffer
			rr := httptest.NewRecorder()
			store.keepGamesyncSession(log.New(&logs, "", 0))(rr, req)
			first, err := readGRPCStreamFrame(rr.Body)
			if err != nil {
				t.Fatal(err)
			}
			change := decodeTestMessage(t, decodeTestMessage(t, first)[3])
			if string(change[1]) != "1" {
				t.Fatal("Different target_id")
			}
			if !own {
				if !bytes.Equal(change[2], []byte{4}) || !bytes.Equal(decodeTestMessage(t, change[3])[1], []byte{7}) {
					t.Fatal("An unauthorized document was not rejected")
				}
				if _, err := readGRPCStreamFrame(rr.Body); err != io.EOF {
					t.Fatal("An unauthorized document was published")
				}
				continue
			}
			if !bytes.Equal(change[2], []byte{1}) {
				t.Fatal("Expected UPDATED first")
			}
			second, err := readGRPCStreamFrame(rr.Body)
			if err != nil {
				t.Fatal(err)
			}
			docChange := decodeTestMessage(t, decodeTestMessage(t, second)[2])
			if string(docChange[1]) != "1" || !bytes.Equal(docChange[2], []byte{1}) {
				t.Fatal("Expected EXIST")
			}
			doc := decodeTestMessage(t, docChange[3])
			if string(doc[1]) != path {
				t.Fatal("Document name changed")
			}
			for _, stamp := range [][]byte{doc[3], doc[4]} {
				sec, ns, err := protoTimeParts(stamp)
				if err != nil || sec != created.Unix() || ns != int32(created.Nanosecond()) {
					t.Fatal("Incorrect ticket timestamp")
				}
			}
			values := map[string][]byte{}
			if err := visitProto(doc[2], func(f, w, _ uint64, v []byte) error {
				if f == 1 && w == 2 {
					entry := decodeTestMessage(t, v)
					values[string(entry[1])] = entry[2]
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if len(values) != 9 {
				t.Fatal("Incomplete UserSession fields")
			}
			if string(decodeTestMessage(t, values["uid"])[7]) != ticket.owner || string(decodeTestMessage(t, values["pgn"])[7]) != "All" {
				t.Fatal("Incorrect identity or group")
			}
			for _, key := range []string{"ussid", "ucsid", "upcsid", "st"} {
				expected := byte(1)
				if key == "st" {
					expected = 2
				}
				if !bytes.Equal(decodeTestMessage(t, values[key])[3], []byte{expected}) {
					t.Fatal("Incorrect integer field: " + key)
				}
			}
			team := ""
			if i == 0 {
				team = "team-a"
			}
			if string(decodeTestMessage(t, values["tn"])[7]) != team {
				t.Fatal("Incorrect team")
			}
			for _, key := range []string{"att", "ltc"} {
				fields := decodeTestMessage(t, values[key])
				value, ok := fields[10]
				if !ok || len(value) != 0 {
					t.Fatal("Incorrectly typed empty map")
				}
			}
			third, err := readGRPCStreamFrame(rr.Body)
			listed := decodeTestMessage(t, decodeTestMessage(t, third)[3])
			if err != nil || string(listed[1]) != "1" || !bytes.Equal(listed[2], []byte{2}) {
				t.Fatal("Missing final LISTED")
			}
			if _, err := readGRPCStreamFrame(rr.Body); err != io.EOF {
				t.Fatal("Unexpected response")
			}
			for _, secret := range []string{access, ticket.owner, id, "private-target", "team-a"} {
				if strings.Contains(logs.String(), secret) {
					t.Fatal("Private content was logged")
				}
			}
			// GetDocument must return exactly the document delivered by the watch.
			getReq := httptest.NewRequest("POST", gamesyncGetDocumentPath, bytes.NewReader(streamTestFrame(protoBytes(nil, 1, []byte(path)))))
			getReq.Header.Set("Content-Type", "application/grpc")
			getReq.Header.Set("Authorization", "Bearer "+access)
			getResult := httptest.NewRecorder()
			store.getGamesyncDocument(log.New(io.Discard, "", 0))(getResult, getReq)
			getResponse := getResult.Result()
			getResponse.Body.Close()
			getDocument, err := readGRPCStreamFrame(getResult.Body)
			if err != nil || getResponse.Trailer.Get("Grpc-Status") != "0" || !bytes.Equal(getDocument, docChange[3]) {
				t.Fatal("GetDocument contradicts the watch")
			}
		}
	}
}
