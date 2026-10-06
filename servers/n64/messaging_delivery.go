package main

import (
	"context"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	msgpb "npln.nintendo.net/npln-practice/proto/messaging/v1"
	"strings"
	"sync"
	"time"
)

type queuedMessage struct {
	response *msgpb.RecvMessageResponse
	expires  time.Time
}

var messageDelivery = struct {
	sync.Mutex
	inboxes  map[string]map[chan *msgpb.RecvMessageResponse]bool
	pending  map[string][]queuedMessage
	sequence uint64
}{inboxes: map[string]map[chan *msgpb.RecvMessageResponse]bool{}, pending: map[string][]queuedMessage{}}

func registerMessageInbox(uid string) (chan *msgpb.RecvMessageResponse, func()) {
	messageDelivery.Lock()
	defer messageDelivery.Unlock()
	ch := make(chan *msgpb.RecvMessageResponse, 128)
	if messageDelivery.inboxes[uid] == nil {
		messageDelivery.inboxes[uid] = map[chan *msgpb.RecvMessageResponse]bool{}
	}
	messageDelivery.inboxes[uid][ch] = true
	for _, q := range messageDelivery.pending[uid] {
		if time.Now().Before(q.expires) {
			ch <- q.response
		}
	}
	delete(messageDelivery.pending, uid)
	return ch, func() {
		messageDelivery.Lock()
		defer messageDelivery.Unlock()
		delete(messageDelivery.inboxes[uid], ch)
		if len(messageDelivery.inboxes[uid]) == 0 {
			delete(messageDelivery.inboxes, uid)
		}
	}
}
func deliverMessages(recipients []string, response *msgpb.RecvMessageResponse) error {
	messageDelivery.Lock()
	defer messageDelivery.Unlock()
	now := time.Now()
	for uid, queue := range messageDelivery.pending {
		kept := queue[:0]
		for _, q := range queue {
			if now.Before(q.expires) {
				kept = append(kept, q)
			}
		}
		if len(kept) == 0 {
			delete(messageDelivery.pending, uid)
		} else {
			messageDelivery.pending[uid] = kept
		}
	}
	// Check every destination before delivery so retrying a full queue cannot partially duplicate a send.
	if len(messageDelivery.pending)+len(recipients) > 4096 {
		return status.Error(codes.ResourceExhausted, "message capacity reached")
	}
	for _, uid := range recipients {
		if len(messageDelivery.inboxes[uid]) == 0 && len(messageDelivery.pending[uid]) >= 128 {
			return status.Error(codes.ResourceExhausted, "recipient queue full")
		}
		for ch := range messageDelivery.inboxes[uid] {
			if len(ch) == cap(ch) {
				return status.Error(codes.ResourceExhausted, "recipient stream full")
			}
		}
	}
	messageDelivery.sequence++
	for _, uid := range recipients {
		r := proto.Clone(response).(*msgpb.RecvMessageResponse)
		token := fmt.Sprint(messageDelivery.sequence)
		if m := r.GetMessage(); m != nil {
			m.MessageResumeToken = token
		}
		if a := r.GetAck(); a != nil {
			a.AckResumeToken = token
		}
		if len(messageDelivery.inboxes[uid]) == 0 {
			messageDelivery.pending[uid] = append(messageDelivery.pending[uid], queuedMessage{r, now.Add(2 * time.Minute)})
		}
		for ch := range messageDelivery.inboxes[uid] {
			ch <- r
		}
	}
	return nil
}
func messageRecipient(name string) (string, error) {
	for _, prefix := range []string{nplnTenant + "/users/", "tenants/current/users/"} {
		if strings.HasPrefix(name, prefix) {
			uid := strings.TrimPrefix(name, prefix)
			if strings.HasPrefix(uid, "u-") && !strings.Contains(uid, "/") {
				return uid, nil
			}
		}
	}
	return "", status.Error(codes.InvalidArgument, "invalid recipient")
}
func (m *messagingServer) SendMessage(ctx context.Context, req *msgpb.SendMessageRequest) (*msgpb.SendMessageResponse, error) {
	claims, err := authenticatedNplnCaller(ctx)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid caller")
	}
	if !messagingUserMatches(req.GetUser(), claims.Subject) {
		return nil, status.Error(codes.PermissionDenied, "sender mismatch")
	}
	body := req.GetMessageBody()
	if body == nil || body.GetMessageRequestId() == "" || proto.Size(body) > 65536 || len(req.GetReceiverUsers()) == 0 || len(req.GetReceiverUsers()) > 100 {
		return nil, status.Error(codes.InvalidArgument, "invalid message")
	}
	allowed := map[string]bool{claims.Subject: true}
	recipients := []string{}
	seen := map[string]bool{}
	for _, name := range req.GetReceiverUsers() {
		uid, e := messageRecipient(name)
		if e != nil {
			return nil, e
		}
		if !seen[uid] {
			recipients = append(recipients, uid)
			seen[uid] = true
		}
	}
	if len(recipients) > 1 || recipients[0] != claims.Subject {
		pid, ok := callerPID(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "account required")
		}
		friends, e := accountFriends(pid)
		if e != nil {
			return nil, status.Error(codes.Unavailable, "friend service unavailable")
		}
		for _, f := range friends.Friends {
			allowed[f.UserID] = true
		}
	}
	for _, uid := range recipients {
		if !allowed[uid] {
			return nil, status.Error(codes.PermissionDenied, "recipient is not a friend")
		}
	}
	response := &msgpb.RecvMessageResponse{Reply: &msgpb.RecvMessageResponse_Message{Message: &msgpb.RecvMessage{MessageBody: body, SenderUser: nplnTenant + "/users/" + claims.Subject, SendTime: timestamppb.Now()}}}
	if err := deliverMessages(recipients, response); err != nil {
		return nil, err
	}
	return &msgpb.SendMessageResponse{}, nil
}
