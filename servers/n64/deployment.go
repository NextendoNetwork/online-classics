package main

import (
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strings"
)

// Deployment mode never silently falls back to the development network or keys.
// Internal account traffic may use a private service network; advertised client
// endpoints must be explicit public IPv4 addresses for this deployment profile.
func validateDeployment(get func(string) string) error {
	mode := strings.TrimSpace(get("NPLN_DEPLOYMENT"))
	if mode == "development" {
		return nil
	}
	if mode != "" && mode != "nextendo" {
		return fmt.Errorf("NPLN_DEPLOYMENT must be nextendo or development")
	}
	for _, key := range []string{"NPLN_GAMESESSION_HOST", "NPLN_LATENCY_HOST", "NPLN_STUN_HOST", "NPLN_TURN_HOST", "NPLN_TURN_RELAY_IP", "NEXTENDO_NNCS1_IP", "NEXTENDO_NNCS2_IP", "NPLN_NNCS_LOCAL_IP"} {
		if !publicDeploymentIPv4(get(key)) {
			return fmt.Errorf("%s requires an explicit public IPv4 address", key)
		}
	}
	if get("NEXTENDO_NNCS1_IP") == get("NEXTENDO_NNCS2_IP") {
		return fmt.Errorf("NNCS requires two distinct assigned public IPv4 addresses")
	}
	if get("NPLN_NNCS_ENABLED") != "1" {
		return fmt.Errorf("NPLN_NNCS_ENABLED must be 1 in Nextendo deployment")
	}
	// The retained verifier treats any nonempty value, including "0", as a bypass.
	if get("NPLN_ALLOW_UNVERIFIED") != "" || envDeploymentEnabled(get("NPLN_ALLOW_LEGACY_SIGNER")) || envDeploymentEnabled(get("NPLN_REGENERATE_CERT")) {
		return fmt.Errorf("unverified authentication and legacy signing are disabled in Nextendo deployment")
	}
	for _, key := range []string{"NEXTENDO_SECRET", "NEXTENDO_INTERNAL_KEY", "NPLN_TURN_PASSWORD"} {
		value := strings.TrimSpace(get(key))
		if len(value) < 16 || value == "3dworld-local-development-only-2026" || value == "change-this-password" {
			return fmt.Errorf("%s requires operator-provisioned secret material", key)
		}
	}
	for _, key := range []string{"NPLN_LISTEN", "NPLN_STUN_LISTEN", "NPLN_TURN_LISTEN", "NPLN_TURN_USERNAME", "NPLN_TURN_REALM"} {
		if strings.TrimSpace(get(key)) == "" {
			return fmt.Errorf("%s must be explicitly configured", key)
		}
	}
	account, err := url.Parse(get("NEXTENDO_ACCOUNT_URL"))
	if err != nil || account.Hostname() == "" || account.User != nil || (account.Scheme != "https" && account.Scheme != "http") {
		return fmt.Errorf("NEXTENDO_ACCOUNT_URL requires an explicit account service URL")
	}
	// Plain HTTP is allowed only for a private, operator-controlled service hop.
	if account.Scheme == "http" {
		ip, err := netip.ParseAddr(account.Hostname())
		if err != nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
			return fmt.Errorf("public account service traffic requires HTTPS")
		}
	}
	for _, key := range []string{"CERT_FILE", "KEY_FILE", "NPLN_JWT_KEY"} {
		path := strings.TrimSpace(get(key))
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("%s must point to existing readable persistent material", key)
		}
		info, err := file.Stat()
		file.Close()
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("%s must point to a nonempty regular file", key)
		}
	}
	certificate, err := tls.LoadX509KeyPair(get("CERT_FILE"), get("KEY_FILE"))
	if err != nil {
		return fmt.Errorf("CERT_FILE/KEY_FILE must contain a valid matching TLS pair")
	}
	profile := get("NPLN_CERT_PROFILE")
	if profile != "minimal" && profile != "observed" && profile != "compat" {
		return fmt.Errorf("NPLN_CERT_PROFILE must be explicitly configured")
	}
	if err := certificateMatchesProfile(certificate, profile); err != nil {
		return fmt.Errorf("CERT_FILE does not match NPLN_CERT_PROFILE")
	}
	keyData, err := os.ReadFile(get("NPLN_JWT_KEY"))
	if err != nil {
		return fmt.Errorf("NPLN_JWT_KEY is unreadable")
	}
	block, _ := pem.Decode(keyData)
	if block == nil {
		return fmt.Errorf("NPLN_JWT_KEY must contain an EC private key")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil || key.Curve != elliptic.P256() {
		return fmt.Errorf("NPLN_JWT_KEY must contain a P-256 EC private key")
	}
	return nil
}

func envDeploymentEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func publicDeploymentIPv4(value string) bool {
	ip, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil || !ip.Is4() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "169.254.0.0/16", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4"} {
		if netip.MustParsePrefix(cidr).Contains(ip) {
			return false
		}
	}
	return true
}
