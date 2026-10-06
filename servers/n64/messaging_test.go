package main

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	msgpb "npln.nintendo.net/npln-practice/proto/messaging/v1"
)

type fakeMessagingStream struct {
	ctx        context.Context
	headerSent bool
	responses  []*msgpb.RecvMessageResponse
}

func (f *fakeMessagingStream) Context() context.Context     { return f.ctx }
func (f *fakeMessagingStream) SetHeader(metadata.MD) error  { return nil }
func (f *fakeMessagingStream) SendHeader(metadata.MD) error { f.headerSent = true; return nil }
func (f *fakeMessagingStream) SetTrailer(metadata.MD)       {}
func (f *fakeMessagingStream) Send(message *msgpb.RecvMessageResponse) error {
	f.responses = append(f.responses, message)
	return nil
}
func (f *fakeMessagingStream) SendMsg(message any) error {
	return f.Send(message.(*msgpb.RecvMessageResponse))
}
func (f *fakeMessagingStream) RecvMsg(any) error { return nil }

var _ proto.Message = (*msgpb.RecvMessageResponse)(nil)

func TestRecvMessageAcceptsObservedCurrentUserAndOpensStream(t *testing.T) {
	uid := "u-test"
	accessToken := mintNplnAccessToken(1800000001, nplnTenant+"/users/"+uid, nplnTenant)
	base := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+accessToken))
	ctx, cancel := context.WithCancel(base)
	cancel()
	stream := &fakeMessagingStream{ctx: ctx}

	err := (&messagingServer{}).RecvMessage(&msgpb.RecvMessageRequest{User: "tenants/current/users/current"}, stream)
	if err != nil {
		t.Fatalf("RecvMessage: %v", err)
	}
	if !stream.headerSent {
		t.Fatal("RecvMessage did not establish response headers")
	}
	if len(stream.responses) != 1 {
		t.Fatalf("responses = %d, want initial keep-alive", len(stream.responses))
	}
	keepAlive := stream.responses[0].GetKeepAlive()
	if keepAlive == nil || keepAlive.GetIdleTimeout().AsDuration() != 95*time.Second {
		t.Fatalf("initial keep-alive = %v, want idle_timeout=95s", keepAlive)
	}
}

func TestRecvMessageRejectsMismatchedUser(t *testing.T) {
	uid := "u-test"
	accessToken := mintNplnAccessToken(1800000001, nplnTenant+"/users/"+uid, nplnTenant)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+accessToken))
	stream := &fakeMessagingStream{ctx: ctx}

	err := (&messagingServer{}).RecvMessage(&msgpb.RecvMessageRequest{User: nplnTenant + "/users/u-other"}, stream)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status = %s, want PermissionDenied", status.Code(err))
	}
}

func TestN64LoginMessageDelivery(t *testing.T) {
	uid := "u-login-test"
	token := mintNplnAccessToken(1800000001, nplnTenant+"/users/"+uid, nplnTenant)
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	req := &msgpb.SendMessageRequest{User: "tenants/current/users/current", ReceiverUsers: []string{"tenants/current/users/" + uid}, MessageBody: &msgpb.MessageBody{MessageRequestId: "login-device", MessageType: "LoginDeviceToken"}}
	if _, err := (&messagingServer{}).SendMessage(ctx, req); err != nil {
		t.Fatal(err)
	}
	ch, done := registerMessageInbox(uid)
	defer done()
	select {
	case response := <-ch:
		if response.GetMessage().GetMessageBody().GetMessageType() != "LoginDeviceToken" || response.GetMessage().GetSenderUser() != nplnTenant+"/users/"+uid {
			t.Fatal(response)
		}
	default:
		t.Fatal("self login message was not delivered")
	}
	req.User = nplnTenant + "/users/u-forged"
	if _, err := (&messagingServer{}).SendMessage(ctx, req); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("sender spoof accepted: %v", err)
	}
	if _, err := messageRecipient("tenants/other/users/" + uid); err == nil {
		t.Fatal("cross tenant recipient accepted")
	}
}
