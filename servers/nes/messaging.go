package main

import (
	"errors"
	"log"
	"net/http"
	"strings"
)

const sendMessagePath = "/nn.npln.messaging.v1.Messaging/SendMessage"

// Logs metadata only. In local-pair mode, delivers LoginDeviceToken to its
// own user; other routes and acknowledgments remain pending.
func (a *labAuth) inspectSendMessage(logger *log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		var user, messageType string
		var messageBody []byte
		receivers := 0
		var receiverNames []string
		var needAck bool
		err = visitProto(payload, func(field, wire, _ uint64, value []byte) error {
			switch field {
			case 1:
				if wire != 2 {
					return errors.New("Invalid user")
				}
				if wire == 2 {
					user = string(value)
				}
			case 2:
				if wire != 2 {
					return errors.New("Invalid recipient")
				}
				if wire == 2 {
					receivers++
					receiverNames = append(receiverNames, string(value))
				}
			case 3:
				if wire != 2 {
					return errors.New("Invalid body")
				}
				if wire == 2 {
					messageBody = append([]byte(nil), value...)
					return visitProto(value, func(innerField, innerWire, _ uint64, innerValue []byte) error {
						if innerField == 4 && innerWire == 2 {
							messageType = string(innerValue)
						}
						return nil
					})
				}
			}
			return nil
		})
		if err != nil || !validUserPath(user, uid) {
			grpcStatus(w, "3", "Invalid message", nil)
			return
		}
		if len(messageType) > 64 {
			messageType = messageType[:64]
		}
		logger.Printf("SendMessage diagnostic: type=%q recipients=%d", messageType, receivers)
		// User references only, as in the search diagnostic;
		// never retain MessageBody, fields, message IDs or device tokens.
		var diagnosticNames []string
		for _, name := range receiverNames {
			prefix := "tenants/" + labTenant + "/users/"
			if strings.HasPrefix(name, "tenants/current/users/") {
				prefix = "tenants/current/users/"
			}
			id := strings.TrimPrefix(name, prefix)
			if strings.HasPrefix(name, prefix) && len(id) <= 128 && gsSafeSegment(id) {
				diagnosticNames = append(diagnosticNames, name)
			} else {
				diagnosticNames = append(diagnosticNames, "<malformed reference>")
			}
		}
		a.identityDiagnostic.record(r, "message", uid, diagnosticNames)
		_ = visitProto(payload, func(f, w, _ uint64, v []byte) error {
			if f == 3 && w == 2 {
				return visitProto(v, func(f, w, n uint64, _ []byte) error {
					if f == 2 && w == 0 {
						needAck = n != 0
					}
					return nil
				})
			}
			return nil
		})
		self, friend, other := 0, 0, 0
		var peers []localFriendPeer
		if a.friendPair != nil {
			peers, _, _ = a.friendPair.snapshot(uid)
		}
		for _, name := range receiverNames {
			if messagingOwnUser(name, uid) {
				self++
				continue
			}
			matched := false
			for _, peer := range peers {
				if name == "tenants/"+labTenant+"/users/"+peer.uid || name == "tenants/current/users/"+peer.uid {
					matched = true
					break
				}
			}
			if matched {
				friend++
			} else {
				other++
			}
		}
		logger.Printf("Messaging routes: client=%q own=%d friends=%d others=%d requires_ack=%t", r.RemoteAddr, self, friend, other, needAck)
		if a.messages != nil && messageType == "LoginDeviceToken" && receivers == 1 && self == 1 && !needAck {
			_, _, known := a.friendPair.snapshot(uid)
			if !known {
				grpcStatus(w, "7", "User outside the local pair", nil)
				return
			}
			if err := validateLocalMessageBody(messageBody); err != nil {
				grpcStatus(w, "3", "Invalid message body", nil)
				return
			}
			if !a.messages.enqueue(uid, messageBody) {
				grpcStatus(w, "8", "Message queue full", nil)
				return
			}
			logger.Printf("Messaging local: client=%q type=LoginDeviceToken recipient=self queued=true", r.RemoteAddr)
			grpcStatus(w, "0", "", nil)
			return
		}
		grpcStatus(w, "12", "Message delivery not implemented", nil)
	}
}
