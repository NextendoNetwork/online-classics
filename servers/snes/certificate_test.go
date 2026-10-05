package main

import (
	"crypto/x509"
	"testing"
)

func TestDevelopmentCertificateCoversObservedNPLNAndGamesyncNames(t *testing.T) {
	cert, err := developmentCertificate()
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"localhost", "127.0.0.1", labTenant + ".lp1.t.npln.srv.nintendo.net", "gamesync.npln.nintendo.net"} {
		if err := leaf.VerifyHostname(name); err != nil {
			t.Fatalf("Certificate does not cover %s: %v", name, err)
		}
	}
	if leaf.VerifyHostname("unrelated.example") == nil {
		t.Fatal("Certificate covers an unrelated name")
	}
}
