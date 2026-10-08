package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/pion/turn/v4"
)

var deploymentTURN *turnServer

type turnAccountCredential struct {
	pid    uint64
	kind   string
	key    []byte
	expiry time.Time
}
type turnAccountRegistry struct {
	mu          sync.Mutex
	credentials map[string]turnAccountCredential
}

func (s *turnServer) accountCredential(pid uint64, kind, realm string) (string, string, error) {
	var nonce, secret [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", "", err
	}
	if _, err := rand.Read(secret[:]); err != nil {
		return "", "", err
	}
	username, password := base64.RawURLEncoding.EncodeToString(nonce[:]), base64.RawURLEncoding.EncodeToString(secret[:])
	registry := s.accounts
	if registry == nil {
		return "", "", errors.New("account TURN unavailable")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	now := time.Now()
	for user, v := range registry.credentials {
		if !v.expiry.After(now) {
			delete(registry.credentials, user)
		}
	}
	if len(registry.credentials) >= 4096 {
		return "", "", errors.New("TURN credential capacity reached")
	}
	registry.credentials[username] = turnAccountCredential{pid: pid, kind: kind, key: turn.GenerateAuthKey(username, realm, password), expiry: now.Add(time.Hour)}
	return username, password, nil
}

func (r *turnAccountRegistry) authenticate(user, realm, expected string, src net.Addr) ([]byte, bool) {
	peer, ok := src.(*net.UDPAddr)
	if !ok || realm != expected {
		return nil, false
	}
	r.mu.Lock()
	credential, exists := r.credentials[user]
	r.mu.Unlock()
	if !exists || !credential.expiry.After(time.Now()) || deploymentAccountVerifier == nil || deploymentAccountIdentity == nil {
		return nil, false
	}
	if !deploymentAccountVerifier.CheckOnline(credential.pid, credential.kind, peer.IP.String()) {
		return nil, false
	}
	if _, err := deploymentAccountIdentity(credential.pid); err != nil {
		return nil, false
	}
	return credential.key, true
}

func productionRelayGenerator(publicIP net.IP) (turn.RelayAddressGenerator, error) {
	bindIP := net.ParseIP(os.Getenv("NPLN_TURN_BIND_IP"))
	if bindIP == nil || bindIP.To4() == nil {
		return nil, errors.New("explicit TURN relay bind IPv4 required")
	}
	minPort, err := strconv.Atoi(os.Getenv("NPLN_TURN_RELAY_MIN_PORT"))
	if err != nil {
		return nil, err
	}
	maxPort, err := strconv.Atoi(os.Getenv("NPLN_TURN_RELAY_MAX_PORT"))
	if err != nil || minPort < 1024 || maxPort < minPort || maxPort > 65535 || maxPort-minPort > 255 {
		return nil, errors.New("bounded TURN relay range required")
	}
	return &turn.RelayAddressGeneratorPortRange{RelayAddress: publicIP, Address: bindIP.String(), MinPort: uint16(minPort), MaxPort: uint16(maxPort), MaxRetries: 32}, nil
}

func accountICETURN(ctx context.Context, user string) (string, string, error) {
	pid, ok := callerPID(ctx)
	if !ok {
		return "", "", errors.New("signed NPLN account token required")
	}
	kind := "ryujinx"
	if consoleFriendsFromContext(ctx) {
		kind = "switch"
	}
	identity, err := gateAccountPID(ctx, pid, kind)
	if err != nil {
		return "", "", err
	}
	if user != "" && user != "tenants/current/users/current" && user != nplnTenant+"/users/"+identity.UserID {
		return "", "", errors.New("ICE user mismatch")
	}
	if deploymentTURN == nil {
		return "", "", errors.New("account TURN not active")
	}
	return deploymentTURN.accountCredential(pid, kind, envOr("NPLN_TURN_REALM", defaultTURNRealm))
}
