package main

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"io"
	"log"
	commonpb "npln.nintendo.net/npln-practice/proto/common"
	friendspb "npln.nintendo.net/npln-practice/proto/friends/v1"
	"strings"
	"sync"
	"time"
)

var livePresences = struct {
	sync.Mutex
	entries     map[string]*friendspb.Presence
	generations map[string]uint64
	next        uint64
}{entries: map[string]*friendspb.Presence{}, generations: map[string]uint64{}}

func presenceName(uid string) string { return nplnTenant + "/users/" + uid + "/presence" }
func openPresence(uid string) uint64 {
	livePresences.Lock()
	defer livePresences.Unlock()
	livePresences.next++
	n := livePresences.next
	livePresences.generations[uid] = n
	livePresences.entries[uid] = &friendspb.Presence{Name: presenceName(uid), State: friendspb.State_ONLINE}
	return n
}
func closePresence(uid string, generation uint64) {
	livePresences.Lock()
	defer livePresences.Unlock()
	if livePresences.generations[uid] != generation {
		return
	}
	livePresences.entries[uid] = &friendspb.Presence{Name: presenceName(uid), State: friendspb.State_OFFLINE, LastOnlineTime: timestamppb.Now()}
}
func updatePresence(uid string, generation uint64, u *friendspb.KeepAliveRequest_UpdatePresence) error {
	if u.GetPresence() == nil {
		return status.Error(codes.InvalidArgument, "presence required")
	}
	incoming := u.GetPresence()
	name := incoming.GetName()
	if name != "" && !strings.HasPrefix(name, "tenants/current/users/current/") && !strings.HasPrefix(name, nplnTenant+"/users/"+uid+"/") {
		return status.Error(codes.PermissionDenied, "presence owner mismatch")
	}
	livePresences.Lock()
	defer livePresences.Unlock()
	if livePresences.generations[uid] != generation {
		return nil
	}
	old := livePresences.entries[uid]
	next := proto.Clone(old).(*friendspb.Presence)
	paths := u.GetUpdateMask().GetPaths()
	if len(paths) == 0 {
		paths = []string{"*"}
	}
	for _, path := range paths {
		switch {
		case path == "*":
			next = proto.Clone(incoming).(*friendspb.Presence)
		case path == "state":
			next.State = incoming.State
		case path == "attributes":
			next.Attributes = proto.Clone(incoming).(*friendspb.Presence).Attributes
		case strings.HasPrefix(path, "attributes."):
			key := strings.TrimPrefix(path, "attributes.")
			if next.Attributes == nil {
				next.Attributes = map[string]*commonpb.Value{}
			}
			if v := incoming.Attributes[key]; v != nil {
				next.Attributes[key] = proto.Clone(v).(*commonpb.Value)
			} else {
				delete(next.Attributes, key)
			}
		case path == "name":
		default:
			return status.Error(codes.InvalidArgument, "unsupported presence update mask")
		}
	}
	next.Name = presenceName(uid)
	if name != "" {
		next.Name = strings.Replace(name, "tenants/current/users/current", nplnTenant+"/users/"+uid, 1)
	}
	if next.State == friendspb.State_STATE_UNSPECIFIED {
		next.State = friendspb.State_ONLINE
	}
	next.Unsubscribed = false
	livePresences.entries[uid] = next
	log.Printf("[NPLN Presence] update uid=%s state=%s attributes=%d", uid, next.State, len(next.Attributes))
	return nil
}
func (p *presenceServer) KeepAlive(stream grpc.BidiStreamingServer[friendspb.KeepAliveRequest, friendspb.KeepAliveResponse]) error {
	ctx := stream.Context()
	uid, err := authenticatedNPLNUID(ctx)
	if err != nil {
		return err
	}
	generation := openPresence(uid)
	defer closePresence(uid, generation)
	reportCtx, cancelReport := context.WithCancel(ctx)
	defer cancelReport()
	if pid, ok := callerPID(ctx); ok {
		go keepAccountPresence(reportCtx, uid, generation, pid)
	}
	if err := stream.Send(&friendspb.KeepAliveResponse{Heartbeat: presenceHeartbeat()}); err != nil {
		return err
	}
	requests := make(chan *friendspb.KeepAliveRequest)
	errs := make(chan error, 1)
	go func() {
		for {
			req, err := stream.Recv()
			if err != nil {
				errs <- err
				return
			}
			select {
			case requests <- req:
			case <-ctx.Done():
				return
			}
		}
	}()
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errs:
			if err == io.EOF {
				return nil
			}
			return err
		case req := <-requests:
			if u := req.GetUpdatePresence(); u != nil {
				if err := updatePresence(uid, generation, u); err != nil {
					return err
				}
			}
		case <-ticker.C:
			if err := stream.Send(&friendspb.KeepAliveResponse{Heartbeat: presenceHeartbeat()}); err != nil {
				return err
			}
		}
	}
}
func presenceSnapshot(friends []nplnFriendData, requested []string) []*friendspb.Presence {
	livePresences.Lock()
	defer livePresences.Unlock()
	out := []*friendspb.Presence{}
	for _, f := range friends {
		p := livePresences.entries[f.UserID]
		if p == nil {
			p = &friendspb.Presence{Name: presenceName(f.UserID), State: friendspb.State_OFFLINE}
		}
		if len(requested) > 0 {
			found := false
			for _, name := range requested {
				if name == p.Name {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		out = append(out, proto.Clone(p).(*friendspb.Presence))
	}
	return out
}
func (p *presenceServer) SubscribePresences(req *friendspb.SubscribePresencesRequest, stream grpc.ServerStreamingServer[friendspb.SubscribePresencesResponse]) error {
	ctx := stream.Context()
	if _, err := authenticatedNPLNUID(ctx); err != nil {
		return err
	}
	pid, ok := callerPID(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "account required")
	}
	me, err := accountFriends(pid)
	if err != nil {
		return status.Error(codes.Unavailable, "friend service unavailable")
	}
	if err := stream.Send(&friendspb.SubscribePresencesResponse{Response: &friendspb.SubscribePresencesResponse_Heartbeat{Heartbeat: presenceHeartbeat()}}); err != nil {
		return err
	}
	previous := map[string]*friendspb.Presence{}
	send := func(initial bool) error {
		all := presenceSnapshot(me.Friends, req.GetPresences())
		changed := []*friendspb.Presence{}
		for _, v := range all {
			if initial || !proto.Equal(v, previous[v.Name]) {
				changed = append(changed, v)
			}
			previous[v.Name] = v
		}
		if initial || len(changed) > 0 {
			return stream.Send(&friendspb.SubscribePresencesResponse{Response: &friendspb.SubscribePresencesResponse_Presences_{Presences: &friendspb.SubscribePresencesResponse_Presences{Presences: changed}}})
		}
		return nil
	}
	if err := send(true); err != nil {
		return err
	}
	if err := stream.Send(&friendspb.SubscribePresencesResponse{Response: &friendspb.SubscribePresencesResponse_EnumerationDone{EnumerationDone: &friendspb.SubscribePresencesResponse_PresenceEnumerationDone{}}}); err != nil {
		return err
	}
	log.Printf("[NPLN Presence] enumeration complete pid=%d friends=%d", pid, len(me.Friends))
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := send(false); err != nil {
				return err
			}
		case <-heartbeat.C:
			if err := stream.Send(&friendspb.SubscribePresencesResponse{Response: &friendspb.SubscribePresencesResponse_Heartbeat{Heartbeat: presenceHeartbeat()}}); err != nil {
				return err
			}
		}
	}
}
