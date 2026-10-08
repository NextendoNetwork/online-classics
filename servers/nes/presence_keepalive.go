package main

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

const presenceKeepAlivePath = "/nn.npln.friends.v1.PresenceService/KeepAlive"

// KeepAlive is bidirectional: do not wait for EOF before sending Heartbeat.
// The local pair publishes only updates from the authenticated user.
func validatePresenceKeepAlive(payload []byte, uid string) (string, error) {
	var kind string
	err := visitProto(payload, func(f, w, _ uint64, v []byte) error {
		if f != 1 && f != 2 {
			return nil
		}
		if w != 2 || kind != "" {
			return errors.New("Invalid operation")
		}
		if f == 2 {
			kind = "ack"
			name, err := protoStringField1(v)
			if err != nil || !validUserPath(name, uid) {
				return errors.New("Incorrect user")
			}
			return nil
		}
		kind = "update"
		var presence []byte
		err := visitProto(v, func(f, w, _ uint64, v []byte) error {
			if f == 1 {
				if w != 2 {
					return errors.New("Invalid presence")
				}
				presence = v
			}
			if f == 2 {
				if w != 2 {
					return errors.New("Invalid mask")
				}
				return visitProto(v, func(f, w, _ uint64, _ []byte) error {
					if f == 1 && w != 2 {
						return errors.New("Invalid mask")
					}
					return nil
				})
			}
			return nil
		})
		if err != nil {
			return err
		}
		name, err := protoStringField1(presence)
		if err != nil || !validUserPath(strings.TrimSuffix(name, "/presence"), uid) {
			return errors.New("Unauthorized or invalid presence")
		}
		return nil
	})
	if err == nil && kind == "" {
		err = errors.New("Missing operation")
	}
	return kind, err
}

func (a *labAuth) keepPresenceAlive(w http.ResponseWriter, r *http.Request) {
	if !validGRPC(w, r) {
		return
	}
	uid, ok := a.authorizedUser(w, r)
	if !ok {
		return
	}
	if a.nextendo != nil {
		claims, _ := a.verifyJWT(splitBearer(r))
		var end func()
		r.Body, end = a.trackAccountStream(claims.Session, r.Body)
		defer end()
	}
	defer r.Body.Close()
	var lease uint64
	if a.presences != nil {
		_, _, known := a.friendSnapshot(r, uid)
		if !known {
			grpcStatus(w, "7", "User outside the local pair", nil)
			return
		}
		lease = a.presences.open(uid)
		defer a.presences.close(uid, lease)
	}
	heartbeat := protoBytes(nil, 1, localPresenceHeartbeat())
	if err := writeGRPCFrame(w, heartbeat); err != nil {
		return
	}
	if a.logger != nil {
		a.logger.Printf("Presence KeepAlive: client=%q initial_heartbeat=true", r.RemoteAddr)
	}
	readDone := make(chan error, 1)
	go func() {
		for {
			payload, err := readGRPCStreamFrame(r.Body)
			if err != nil {
				readDone <- err
				return
			}
			kind, err := validatePresenceKeepAlive(payload, uid)
			if err != nil {
				readDone <- err
				return
			}
			if a.logger != nil {
				a.logger.Printf("Presence KeepAlive: client=%q operation=%s validated=true", r.RemoteAddr, kind)
			}
			if kind == "update" && a.presences != nil {
				if err := a.presences.update(uid, lease, payload); err != nil {
					readDone <- err
					return
				}
				if a.logger != nil {
					a.logger.Printf("Presence local: client=%q update_published=true", r.RemoteAddr)
				}
			}
		}
	}()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case err := <-readDone:
			if err == io.EOF {
				w.Header().Set("Grpc-Status", "0")
			} else {
				w.Header().Set("Grpc-Status", "3")
				w.Header().Set("Grpc-Message", "Invalid KeepAlive")
			}
			return
		case <-ticker.C:
			if a.nextendo != nil {
				if _, ok := a.authorizedUser(w, r); !ok {
					return
				}
			}
			if writeGRPCFrame(w, heartbeat) != nil {
				return
			}
		}
	}
}
