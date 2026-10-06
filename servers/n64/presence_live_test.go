package main

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"net/http"
	"net/http/httptest"
	commonpb "npln.nintendo.net/npln-practice/proto/common"
	friendspb "npln.nintendo.net/npln-practice/proto/friends/v1"
	"testing"
)

func TestPresenceUpdatesAreScopedAndSurviveOldDisconnect(t *testing.T) {
	uid := "presence-test-host"
	old := openPresence(uid)
	current := openPresence(uid)
	u := &friendspb.KeepAliveRequest_UpdatePresence{Presence: &friendspb.Presence{Name: "tenants/current/users/current/presence", Attributes: map[string]*commonpb.Value{"room": gamesyncStringValue("room-a")}}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"attributes"}}}
	if err := updatePresence(uid, current, u); err != nil {
		t.Fatal(err)
	}
	closePresence(uid, old)
	friends := []nplnFriendData{{UserID: uid}, {UserID: "offline-test"}}
	snap := presenceSnapshot(friends, nil)
	if len(snap) != 2 || snap[0].State != friendspb.State_ONLINE || snap[0].Attributes["room"].GetStringValue() != "room-a" || snap[1].State != friendspb.State_OFFLINE {
		t.Fatalf("bad snapshot %v", snap)
	}
	if len(presenceSnapshot([]nplnFriendData{}, nil)) != 0 {
		t.Fatal("nonfriend data exposed")
	}
	u.Presence.Name = presenceName("someone-else")
	if status.Code(updatePresence(uid, current, u)) != codes.PermissionDenied {
		t.Fatal("forged owner accepted")
	}
	closePresence(uid, current)
	snap = presenceSnapshot(friends, nil)
	if snap[0].State != friendspb.State_OFFLINE || len(snap[0].Attributes) != 0 {
		t.Fatal("stale joinable presence retained")
	}
}

type presenceCapture struct {
	grpc.ServerStream
	ctx       context.Context
	cancel    context.CancelFunc
	responses []*friendspb.SubscribePresencesResponse
}

func (s *presenceCapture) Context() context.Context { return s.ctx }
func (s *presenceCapture) Send(r *friendspb.SubscribePresencesResponse) error {
	s.responses = append(s.responses, r)
	if r.GetEnumerationDone() != nil {
		s.cancel()
	}
	return nil
}
func TestPresenceEnumerationCompletesEvenWithNoFriends(t *testing.T) {
	account := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"friends":[]}`))
	}))
	defer account.Close()
	old := accountBaseURL
	accountBaseURL = account.URL
	defer func() { accountBaseURL = old }()
	ctx, cancel := context.WithCancel(callerContext("u-presence-reader"))
	defer cancel()
	stream := &presenceCapture{ctx: ctx, cancel: cancel}
	if err := (&presenceServer{}).SubscribePresences(&friendspb.SubscribePresencesRequest{}, stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.responses) != 3 || stream.responses[1].GetPresences() == nil || stream.responses[2].GetEnumerationDone() == nil {
		t.Fatalf("missing enumeration completion %v", stream.responses)
	}
}
