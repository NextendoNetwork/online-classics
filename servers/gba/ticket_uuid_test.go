package main

import (
	"regexp"
	"testing"
)

func TestTicketUUIDRoundTrip(t *testing.T) {
	id, err := randomUUID()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Fatalf("Invalid UUID v4: %q", id)
	}
	name, form := canonicalTicketName("tenants/current/gameSessionCreationTickets/" + id)
	if form != "current" || name != "tenants/"+labTenant+"/gameSessionCreationTickets/"+id {
		t.Fatalf("Unresolved alias: %q %q", name, form)
	}
}
