package main

import (
	"bytes"
	"testing"
	"time"
)

func TestSucceededSessionCreationTimesAreStableAndEqual(t *testing.T) {
	created := time.Unix(1800000000, 123456789).UTC()
	ticket := pendingTicket{createdAt: created, owner: "u-test", sessionName: "tenants/" + labTenant + "/gameSessions/test", userName: "tenants/" + labTenant + "/gameSessions/test/userSessions/test"}
	readField := func(data []byte, want uint64) []byte {
		t.Helper()
		var found []byte
		err := visitProto(data, func(field, wire, _ uint64, value []byte) error {
			if field == want && wire == 2 {
				found = value
			}
			return nil
		})
		if err != nil || found == nil {
			t.Fatalf("Missing field %d: %v", want, err)
		}
		return found
	}
	response := ticketSucceededResponse("ticket", ticket, "token")
	session := readField(response, 6)
	sessionTime := readField(session, 10)
	participantTime := readField(readField(session, 12), 4)
	if !bytes.Equal(sessionTime, participantTime) {
		t.Fatal("Dates do not match")
	}
	seconds, nanos, err := protoTimeParts(sessionTime)
	if err != nil || seconds != created.Unix() || int(nanos) != created.Nanosecond() {
		t.Fatal("Incorrect protobuf timestamp")
	}
	if !bytes.Equal(response, ticketSucceededResponse("ticket", ticket, "token")) {
		t.Fatal("Track changed creation timestamp")
	}
}
