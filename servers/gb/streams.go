package main

import (
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	subscribeMaintenancePath = "/nn.npln.maintenance.v1.MaintenanceScheduleService/SubscribeMaintenanceSchedules"
	subscribeFriendsPath     = "/nn.npln.friends.v1.Friends/SubscribeFriendUsers"
	recvMessagePath          = "/nn.npln.messaging.v1.Messaging/RecvMessage"
	subscribePresencesPath   = "/nn.npln.friends.v1.PresenceService/SubscribePresences"
)

func grpcRequestPayload(r *http.Request) ([]byte, error) {
	frame, err := io.ReadAll(io.LimitReader(r.Body, 64*1024+6))
	if err != nil || len(frame) < 5 || len(frame) > 64*1024+5 || frame[0] != 0 ||
		int(binary.BigEndian.Uint32(frame[1:5])) != len(frame)-5 {
		return nil, errors.New("Invalid gRPC frame")
	}
	return frame[5:], nil
}

func protoStringField1(payload []byte) (string, error) {
	var result string
	err := visitProto(payload, func(field, wire, _ uint64, value []byte) error {
		if field == 1 {
			if wire != 2 {
				return errors.New("Field 1 is not a string")
			}
			result = string(value)
		}
		return nil
	})
	if result == "" && err == nil {
		err = errors.New("Field 1 is empty")
	}
	return result, err
}

func validGRPC(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
		http.NotFound(w, r)
		return false
	}
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
	if r.Header.Get("npln-tenant-id") != labTenant {
		grpcStatus(w, "3", "Incorrect tenant", nil)
		return false
	}
	return true
}

func (a *labAuth) authorizedUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	parts := strings.Fields(r.Header.Get("authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		grpcStatus(w, "16", "Missing local token", nil)
		return "", false
	}
	claims, valid := a.verifyJWT(parts[1])
	if !valid || (r.Header.Get("uid") != "" && r.Header.Get("uid") != claims.Subject) {
		grpcStatus(w, "16", "Invalid local token", nil)
		return "", false
	}
	return claims.Subject, true
}

func validUserPath(name, uid string) bool {
	return name == "tenants/current/users/current" || name == "tenants/"+labTenant+"/users/"+uid
}

func writeGRPCFrame(w http.ResponseWriter, payload []byte) error {
	frame := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	frame = append(frame, payload...)
	_, err := w.Write(frame)
	if flush, ok := w.(http.Flusher); ok {
		flush.Flush()
	}
	return err
}

// A gRPC stream sends an initial response and stays open until the client
// cancels it. The ticker prevents the client from treating the channel as idle.
func serveGRPCStream(w http.ResponseWriter, r *http.Request, first, heartbeat []byte) {
	serveGRPCStreamSequence(w, r, [][]byte{first}, heartbeat, 45*time.Second)
}

func serveGRPCStreamSequence(w http.ResponseWriter, r *http.Request, initial [][]byte, heartbeat []byte, interval time.Duration) {
	w.WriteHeader(http.StatusOK)
	for _, frame := range initial {
		if writeGRPCFrame(w, frame) != nil {
			return
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if writeGRPCFrame(w, heartbeat) != nil {
				return
			}
		}
	}
}

func (a *labAuth) subscribePresences(w http.ResponseWriter, r *http.Request) {
	if !validGRPC(w, r) {
		return
	}
	uid, ok := a.authorizedUser(w, r)
	if !ok {
		return
	}
	payload, err := grpcRequestPayload(r)
	if err != nil {
		grpcStatus(w, "3", "Invalid frame", nil)
		return
	}
	user, err := protoStringField1(payload)
	if err != nil || !validUserPath(user, uid) {
		grpcStatus(w, "3", "Incorrect user", nil)
		return
	}
	if a.presences != nil && a.friendPair != nil {
		a.subscribeLocalPresences(w, r, uid, payload)
		return
	}
	// Initial response observed by Nextendo for a user without friends:
	// Heartbeat (30-second interval, 50-second deadline) and EnumerationDone,
	// without an intermediate empty presence list.
	heartbeatData := protoBytes(nil, 1, protoVarint(nil, 1, 30))
	heartbeatData = protoBytes(heartbeatData, 2, protoVarint(nil, 1, 50))
	heartbeat := protoBytes(nil, 3, heartbeatData)
	done := protoBytes(nil, 2, nil)
	serveGRPCStreamSequence(w, r, [][]byte{heartbeat, done}, heartbeat, 30*time.Second)
}

func subscribeMaintenance(w http.ResponseWriter, r *http.Request) {
	if !validGRPC(w, r) {
		return
	}
	payload, err := grpcRequestPayload(r)
	if err != nil {
		grpcStatus(w, "3", "Invalid frame", nil)
		return
	}
	tenant, err := protoStringField1(payload)
	if err != nil || (tenant != "tenants/"+labTenant && tenant != "tenants/current") {
		grpcStatus(w, "3", "Incorrect tenant", nil)
		return
	}
	// Field 2 = empty KeepAlive: maintenance windows are not implemented yet.
	keepAlive := protoBytes(nil, 2, nil)
	serveGRPCStream(w, r, keepAlive, keepAlive)
}

func (a *labAuth) subscribeFriends(w http.ResponseWriter, r *http.Request) {
	if !validGRPC(w, r) {
		return
	}
	uid, ok := a.authorizedUser(w, r)
	if !ok {
		return
	}
	payload, err := grpcRequestPayload(r)
	if err != nil {
		grpcStatus(w, "3", "Invalid frame", nil)
		return
	}
	user, err := protoStringField1(payload)
	if err != nil || !validUserPath(user, uid) {
		grpcStatus(w, "3", "Incorrect user", nil)
		return
	}
	// Field 3 = keep_alive_interval; do not invent friends.
	if a.friendPair != nil {
		a.friendPair.serveSubscription(w, r, uid, a.logger)
		return
	}
	interval := protoBytes(nil, 3, protoVarint(nil, 1, 50))
	serveGRPCStream(w, r, interval, interval)
}

func (a *labAuth) recvMessage(w http.ResponseWriter, r *http.Request) {
	if !validGRPC(w, r) {
		return
	}
	uid, ok := a.authorizedUser(w, r)
	if !ok {
		return
	}
	payload, err := grpcRequestPayload(r)
	if err != nil {
		grpcStatus(w, "3", "Invalid frame", nil)
		return
	}
	user, err := protoStringField1(payload)
	if err != nil || !validUserPath(user, uid) {
		grpcStatus(w, "3", "Incorrect user", nil)
		return
	}
	// RecvMessageResponse.keep_alive = 3; the first message announces 95 seconds.
	if a.messages != nil {
		a.recvLocalMessages(w, r, uid, payload)
		return
	}
	first := protoBytes(nil, 3, protoBytes(nil, 1, protoVarint(nil, 1, 95)))
	heartbeat := protoBytes(nil, 3, protoBytes(nil, 1, nil))
	serveGRPCStream(w, r, first, heartbeat)
}
