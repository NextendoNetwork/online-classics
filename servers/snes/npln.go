package main

import (
	"encoding/binary"
	"net/http"
	"sort"
	"strings"
)

const (
	queryGameSessionsPath = "/nn.npln.matchmaking.v1.GameSessionService/QueryGameSessions"
	labTenant             = "t-4bdb1dd3-lp1"
)

// This response uses gRPC framing and the published protobuf fields of
// QueryGameSessionsResponse. An empty request belongs to the local simulator;
// a real query returns an empty list until actual NPLN sessions exist.
func (s *roomStore) queryGameSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
	if r.Header.Get("npln-tenant-id") != labTenant {
		grpcStatus(w, "3", "Incorrect test tenant", nil)
		return
	}
	payload, err := grpcRequestPayload(r)
	if err != nil {
		grpcStatus(w, "3", "Invalid frame", nil)
		return
	}
	if len(payload) != 0 {
		tenant, err := protoStringField1(payload)
		if err != nil || (tenant != "tenants/"+labTenant && tenant != "tenants/current") {
			grpcStatus(w, "3", "Incorrect tenant", nil)
			return
		}
		grpcStatus(w, "0", "", nil)
		return
	}

	rooms := s.snapshot()
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].ID < rooms[j].ID })
	var response []byte
	for _, room := range rooms {
		var session []byte
		session = protoBytes(session, 1, []byte("tenants/"+labTenant+"/gameSessions/"+room.ID))
		session = protoVarint(session, 2, 2) // Exercise capacity.
		session = protoVarint(session, 3, uint64(len(room.Players)))
		if len(room.Players) < 2 {
			session = protoVarint(session, 4, 1) // Another player may join.
		}
		session = protoVarint(session, 7, 2) // ACTIVE state.
		response = protoBytes(response, 1, session)
	}
	grpcStatus(w, "0", "", response)
}

func protoVarint(dst []byte, field, value uint64) []byte {
	dst = binary.AppendUvarint(dst, field<<3)
	return binary.AppendUvarint(dst, value)
}

func protoBytes(dst []byte, field uint64, value []byte) []byte {
	dst = binary.AppendUvarint(dst, field<<3|2)
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func grpcStatus(w http.ResponseWriter, status, message string, payload []byte) {
	w.WriteHeader(http.StatusOK)
	if status == "0" {
		frame := make([]byte, 5, 5+len(payload))
		binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
		frame = append(frame, payload...)
		_, _ = w.Write(frame)
	}
	w.Header().Set("Grpc-Status", status)
	if message != "" {
		w.Header().Set("Grpc-Message", message)
	}
}
