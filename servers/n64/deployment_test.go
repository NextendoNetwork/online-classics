package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNextendoDeploymentRejectsLocalAndExampleEndpoints(t *testing.T) {
	for _, value := range []string{"", "10.77.20.3", "10.0.0.3", "172.16.0.3", "127.0.0.1", "0.0.0.0", "100.64.1.2", "169.254.1.2", "192.0.2.1", "198.51.100.1", "203.0.113.1", "198.18.0.1", "255.255.255.255", "::1", "localhost"} {
		if publicDeploymentIPv4(value) {
			t.Errorf("accepted non-deployable client destination %q", value)
		}
	}
}

func TestNextendoDeploymentRequiresProvisioningAndNoBypass(t *testing.T) {
	file := filepath.Join(t.TempDir(), "operator-material")
	if err := os.WriteFile(file, []byte("test fixture; not actual signing material"), 0600); err != nil {
		t.Fatal(err)
	}
	config := map[string]string{
		"NPLN_DEPLOYMENT": "nextendo", "NEXTENDO_NNCS2_IP": "9.9.9.9",
		"NPLN_NNCS_ENABLED": "1", "NEXTENDO_ACCOUNT_URL": "https://accounts.example.invalid",
		"NPLN_LISTEN": "0.0.0.0:443", "NPLN_STUN_LISTEN": "0.0.0.0:3478", "NPLN_TURN_LISTEN": "0.0.0.0:3479",
		"NPLN_TURN_USERNAME": "operator", "NPLN_TURN_REALM": "operator-realm",
		"NPLN_TURN_BIND_IP": "0.0.0.0", "NPLN_TURN_RELAY_MIN_PORT": "48000", "NPLN_TURN_RELAY_MAX_PORT": "48063",
	}
	for _, key := range []string{"NPLN_GAMESESSION_HOST", "NPLN_LATENCY_HOST", "NPLN_STUN_HOST", "NPLN_TURN_HOST", "NPLN_TURN_RELAY_IP", "NEXTENDO_NNCS1_IP", "NPLN_NNCS_LOCAL_IP"} {
		config[key] = "8.8.8.8" // Validation only: never contacted or bound.
	}
	for _, key := range []string{"NEXTENDO_SECRET", "NEXTENDO_INTERNAL_KEY", "NPLN_TURN_PASSWORD"} {
		config[key] = "test-only-secret-material"
	}
	for _, key := range []string{"CERT_FILE", "KEY_FILE", "NPLN_JWT_KEY", "NPLN_ACCOUNT_CONFIG"} {
		config[key] = file
	}
	tlsKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	dns, _ := certificateProfileSANs("observed")
	issuer := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(72 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	issuerDER, err := x509.CreateCertificate(rand.Reader, issuer, issuer, &tlsKey.PublicKey, tlsKey)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour), DNSNames: dns}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &tlsKey.PublicKey, tlsKey)
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(t.TempDir(), "cert.pem"), filepath.Join(t.TempDir(), "key.pem")
	if err := writeCertificatePair(certPath, keyPath, der, issuerDER, tlsKey); err != nil {
		t.Fatal(err)
	}
	config["CERT_FILE"], config["KEY_FILE"], config["NPLN_CERT_PROFILE"] = certPath, keyPath, "observed"
	signingKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signingDER, err := x509.MarshalECPrivateKey(signingKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: signingDER}), 0600); err != nil {
		t.Fatal(err)
	}
	get := func(key string) string { return config[key] }
	if err := validateDeployment(get); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, value, want string }{
		{"NPLN_GAMESESSION_HOST", "10.77.20.3", "NPLN_GAMESESSION_HOST"},
		{"NPLN_TURN_RELAY_IP", "127.0.0.1", "NPLN_TURN_RELAY_IP"},
		{"NPLN_ALLOW_UNVERIFIED", "0", "unverified"},
		{"NEXTENDO_SECRET", "3dworld-local-development-only-2026", "NEXTENDO_SECRET"},
		{"NPLN_TURN_PASSWORD", "change-this-password", "NPLN_TURN_PASSWORD"},
		{"NEXTENDO_ACCOUNT_URL", "http://8.8.8.8", "HTTPS"},
		{"NPLN_JWT_KEY", filepath.Join(t.TempDir(), "missing"), "NPLN_JWT_KEY"},
		{"KEY_FILE", file, "TLS pair"},
	} {
		old := config[tc.key]
		config[tc.key] = tc.value
		if err := validateDeployment(get); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: expected %s rejection, got %v", tc.key, tc.want, err)
		}
		config[tc.key] = old
	}
	config["NPLN_DEPLOYMENT"] = "development"
	config["NPLN_GAMESESSION_HOST"] = "127.0.0.1"
	if err := validateDeployment(get); err != nil {
		t.Fatal("explicit development mode rejected:", err)
	}
	if err := validateDeployment(func(string) string { return "" }); err == nil {
		t.Fatal("unconfigured startup silently entered development mode")
	}
}
