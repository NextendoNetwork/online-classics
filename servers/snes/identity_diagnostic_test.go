package main

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIdentityDiagnosticRecordsOnlyMappingFields(t *testing.T) {
	var out bytes.Buffer
	d := &identityDiagnostic{out: &out}
	r := httptest.NewRequest("POST", "/", strings.NewReader("PRIVATE_BODY"))
	r.RemoteAddr = "192.168.100.9:55000"
	r.Header.Set("Authorization", "Bearer PRIVATE_TOKEN")
	r.Header.Set("Cookie", "PRIVATE_COOKIE")
	d.record(r, "query", "local-guest", []string{"tenants/current/users/peer"})
	if strings.Contains(out.String(), "PRIVATE") || !strings.Contains(out.String(), "192.168.100.9") || !strings.Contains(out.String(), "local-guest") || !strings.Contains(out.String(), "wanted_users") {
		t.Fatal("Diagnostic incomplete or contains credentials")
	}
}
