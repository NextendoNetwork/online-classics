package main

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
)

type connectionKey struct{}

type connectionObservation struct {
	id       uint64
	requests atomic.Uint64
}

// Observes transport without reading bodies, headers, certificates or tokens.
// HTTP/2 StateActive does not prove a call exists: count calls in the handler.
type transportObserver struct {
	logger      *log.Logger
	mu          sync.Mutex
	next        uint64
	connections map[net.Conn]*connectionObservation
}

func newTransportObserver(logger *log.Logger) *transportObserver {
	return &transportObserver{logger: logger, connections: make(map[net.Conn]*connectionObservation)}
}

func (o *transportObserver) context(ctx context.Context, conn net.Conn) context.Context {
	o.mu.Lock()
	o.next++
	entry := &connectionObservation{id: o.next}
	o.connections[conn] = entry
	o.mu.Unlock()
	return context.WithValue(ctx, connectionKey{}, entry)
}

func (o *transportObserver) state(conn net.Conn, state http.ConnState) {
	if state != http.StateNew && state != http.StateClosed && state != http.StateHijacked {
		return
	}
	o.mu.Lock()
	entry := o.connections[conn]
	if state != http.StateNew {
		delete(o.connections, conn)
	}
	o.mu.Unlock()
	if entry == nil {
		return
	}
	if state == http.StateNew {
		o.logger.Printf("transport: connection=%d open", entry.id)
		return
	}
	var complete bool
	var alpn string
	var sni string
	var version uint16
	if secure, ok := conn.(interface{ ConnectionState() tls.ConnectionState }); ok {
		negotiated := secure.ConnectionState()
		complete, alpn = negotiated.HandshakeComplete, negotiated.NegotiatedProtocol
		sni, version = negotiated.ServerName, negotiated.Version
	}
	o.logger.Printf("transport: connection=%d state=%s tls_complete=%t alpn=%q requests=%d sni=%q version=0x%04x",
		entry.id, state, complete, alpn, entry.requests.Load(), sni, version)
}

func (o *transportObserver) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if entry, ok := r.Context().Value(connectionKey{}).(*connectionObservation); ok {
			if entry.requests.Add(1) == 1 {
				o.logger.Printf("transport: connection=%d first_request protocol=%s", entry.id, r.Proto)
			}
		}
		next.ServeHTTP(w, r)
	})
}
