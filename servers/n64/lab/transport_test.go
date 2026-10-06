package main

import (
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type eventWriter chan string

func (events eventWriter) Write(p []byte) (int, error) {
	events <- string(p)
	return len(p), nil
}

func TestTransportDistinguishesTLSOnlyFromHTTPRequest(t *testing.T) {
	events := make(eventWriter, 64)
	observer := newTransportObserver(log.New(events, "", 0))
	server := httptest.NewUnstartedServer(observer.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})))
	server.EnableHTTP2 = true
	server.Config.ConnContext = observer.context
	server.Config.ConnState = observer.state
	server.StartTLS()
	defer server.Close()

	// Ephemeral httptest certificate, used only by this loopback test.
	conn, err := tls.Dial("tcp", server.Listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	waitFor := func(fragment string) {
		t.Helper()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		for {
			select {
			case event := <-events:
				if strings.Contains(event, fragment) {
					return
				}
			case <-timer.C:
				t.Fatalf("Missing event %q", fragment)
			}
		}
	}
	waitFor("tls_complete=true alpn=\"h2\" requests=0")
	response, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.ProtoMajor != 2 {
		t.Fatal("Expected HTTP/2")
	}
	server.Client().CloseIdleConnections()
	waitFor("tls_complete=true alpn=\"h2\" requests=1")
}
