package main

import (
	"errors"
	"net/http"
	"sync"
	"time"
)

func messagingOwnUser(name, uid string) bool {
	return validUserPath(name, uid) || name == "tenants/current/users/"+uid
}
func validateLocalMessageBody(body []byte) error {
	return visitProto(body, func(f, w, n uint64, v []byte) error {
		switch f {
		case 1, 4:
			if w != 2 {
				return errors.New("Invalid string")
			}
		case 2:
			if w != 0 || n > 1 {
				return errors.New("Invalid ack")
			}
		case 3:
			if w != 2 {
				return errors.New("Invalid fields")
			}
			return visitProto(v, func(_, _, _ uint64, _ []byte) error { return nil })
		}
		return nil
	})
}

type localMessage struct {
	body []byte
	sent time.Time
}
type localMailbox struct {
	queue   []localMessage
	changed chan struct{}
	active  bool
}
type localMessageStore struct {
	mu    sync.Mutex
	boxes map[string]*localMailbox
}

func newLocalMessageStore() *localMessageStore {
	return &localMessageStore{boxes: make(map[string]*localMailbox)}
}
func (s *localMessageStore) box(uid string) *localMailbox {
	b := s.boxes[uid]
	if b == nil {
		b = &localMailbox{changed: make(chan struct{})}
		s.boxes[uid] = b
	}
	return b
}
func pruneLocalMessages(b *localMailbox) {
	for len(b.queue) > 0 && time.Since(b.queue[0].sent) > time.Minute {
		b.queue[0] = localMessage{}
		b.queue = b.queue[1:]
	}
}
func (s *localMessageStore) enqueue(uid string, body []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.box(uid)
	pruneLocalMessages(b)
	if len(b.queue) >= 8 {
		return false
	}
	b.queue = append(b.queue, localMessage{body: append([]byte(nil), body...), sent: time.Now()})
	close(b.changed)
	b.changed = make(chan struct{})
	return true
}
func (s *localMessageStore) open(uid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.box(uid)
	if b.active {
		return false
	}
	b.active = true
	return true
}
func (s *localMessageStore) close(uid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.box(uid)
	b.active = false
	clear(b.queue)
	b.queue = nil
}
func (s *localMessageStore) next(uid string) (localMessage, bool, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.box(uid)
	pruneLocalMessages(b)
	if len(b.queue) == 0 {
		return localMessage{}, false, b.changed
	}
	m := b.queue[0]
	b.queue[0] = localMessage{}
	b.queue = b.queue[1:]
	return m, true, b.changed
}
func (a *labAuth) recvLocalMessages(w http.ResponseWriter, r *http.Request, uid string, payload []byte) {
	_, _, known := a.friendPair.snapshot(uid)
	if !known {
		grpcStatus(w, "7", "User outside the local pair", nil)
		return
	}
	resume := false
	err := visitProto(payload, func(f, w, _ uint64, v []byte) error {
		if f == 2 || f == 3 {
			if w != 2 {
				return errors.New("Invalid cursor")
			}
			resume = resume || len(v) > 0
		}
		return nil
	})
	if err != nil {
		grpcStatus(w, "3", "Invalid query", nil)
		return
	}
	if resume {
		grpcStatus(w, "12", "Message resumption not implemented", nil)
		return
	}
	if !a.messages.open(uid) {
		grpcStatus(w, "9", "Local receiver already exists", nil)
		return
	}
	defer a.messages.close(uid)
	first := protoBytes(nil, 3, protoBytes(nil, 1, protoVarint(nil, 1, 95)))
	if writeGRPCFrame(w, first) != nil {
		return
	}
	ticker := time.NewTicker(45 * time.Second)
	defer ticker.Stop()
	for {
		message, ok, changed := a.messages.next(uid)
		if ok {
			// Deliver the unchanged body only to the same user who sent it.
			// Never log its contents or reflect it to another client.
			data := protoBytes(nil, 1, message.body)
			data = protoBytes(data, 2, []byte("tenants/"+labTenant+"/users/"+uid))
			data = protoBytes(data, 4, protoTimestamp(message.sent))
			if writeGRPCFrame(w, protoBytes(nil, 1, data)) != nil {
				return
			}
			if a.logger != nil {
				a.logger.Printf("Messaging local: client=%q type=LoginDeviceToken recipient=self delivered=true", r.RemoteAddr)
			}
			continue
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-ticker.C:
			if writeGRPCFrame(w, protoBytes(nil, 3, protoBytes(nil, 1, nil))) != nil {
				return
			}
		}
	}
}
