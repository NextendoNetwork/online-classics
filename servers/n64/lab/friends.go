package main

import (
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"strings"
)

const activateUserPath = "/nn.npln.friends.v1.Friends/ActivateUser"

// Activates a local session for the user who just authenticated. This does
// not yet create a friends list or publish presence to other consoles.
func (a *labAuth) activateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
	if r.Header.Get("npln-tenant-id") != labTenant {
		grpcStatus(w, "3", "Incorrect tenant", nil)
		return
	}
	authorization := strings.Fields(r.Header.Get("authorization"))
	if len(authorization) != 2 || !strings.EqualFold(authorization[0], "bearer") {
		grpcStatus(w, "16", "Missing local token", nil)
		return
	}
	claims, valid := a.verifyJWT(authorization[1])
	if !valid || (r.Header.Get("uid") != "" && r.Header.Get("uid") != claims.Subject) {
		grpcStatus(w, "16", "Invalid local token", nil)
		return
	}

	frame, err := io.ReadAll(io.LimitReader(r.Body, 64*1024+6))
	if err != nil || len(frame) < 5 || len(frame) > 64*1024+5 || frame[0] != 0 ||
		int(binary.BigEndian.Uint32(frame[1:5])) != len(frame)-5 {
		grpcStatus(w, "3", "Invalid gRPC frame", nil)
		return
	}
	var name string
	err = visitProto(frame[5:], func(field, wire, _ uint64, value []byte) error {
		if field == 1 {
			if wire != 2 {
				return errors.New("Name is not a string")
			}
			name = string(value)
		}
		return nil
	})
	if err != nil || (name != "tenants/current/users/current" &&
		name != "tenants/"+labTenant+"/users/"+claims.Subject) {
		grpcStatus(w, "3", "Incorrect user", nil)
		return
	}
	grpcStatus(w, "0", "", nil)
}
