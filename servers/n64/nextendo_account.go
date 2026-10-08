package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"

	"github.com/NextendoNetwork/online-classics/accountauth"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	authpb "npln.nintendo.net/npln-practice/proto/auth/v1"
)

var deploymentAccountVerifier *accountauth.NextendoAuth
var deploymentAccountIdentity func(uint64) (accountauth.Identity, error)

func configureAccountVerifier() error {
	if os.Getenv("NPLN_DEPLOYMENT") == "development" {
		return nil
	}
	file, err := os.Open(os.Getenv("NPLN_ACCOUNT_CONFIG"))
	if err != nil {
		return errors.New("NPLN_ACCOUNT_CONFIG private file required")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 65537))
	decoder.DisallowUnknownFields()
	var cfg accountauth.NextendoConfig
	if decoder.Decode(&cfg) != nil || decoder.Decode(new(any)) != io.EOF || cfg.TitleID != nplnAppID || !cfg.AllowAllAccounts {
		return errors.New("exact N64 account scope and open enrollment required")
	}
	verifier, err := accountauth.NewNextendoAuth(cfg, nil)
	if err != nil {
		return err
	}
	identity, err := accountauth.IdentityClient(cfg)
	if err != nil {
		return err
	}
	deploymentAccountVerifier, deploymentAccountIdentity = verifier, identity
	return nil
}

func accountPeerIP(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	return ip.String()
}

func gateAccountPID(ctx context.Context, pid uint64, kind string) (accountauth.Identity, error) {
	if deploymentAccountVerifier == nil || deploymentAccountIdentity == nil {
		return accountauth.Identity{}, status.Error(codes.Unauthenticated, "account verifier unavailable")
	}
	if !deploymentAccountVerifier.CheckOnline(pid, kind, accountPeerIP(ctx)) {
		return accountauth.Identity{}, status.Error(codes.PermissionDenied, "account gate rejected")
	}
	identity, err := deploymentAccountIdentity(pid)
	if err != nil {
		return accountauth.Identity{}, status.Error(codes.Unauthenticated, "account identity rejected")
	}
	return identity, nil
}

func authenticatedIdentity(ctx context.Context, ext *authpb.ExternalIdToken, tenant string) (uint64, string, error) {
	if os.Getenv("NPLN_DEPLOYMENT") == "development" {
		return gatedIdentity(ext, tenant)
	}
	if deploymentAccountVerifier == nil || ext == nil || (tenant != "" && tenant != nplnTenant) {
		return 0, "", status.Error(codes.Unauthenticated, "signed Nextendo credential required")
	}
	token := ext.GetNsaIdToken()
	pid, err := deploymentAccountVerifier.Authenticate(token, accountPeerIP(ctx))
	if err != nil {
		return 0, "", status.Error(codes.Unauthenticated, "Nextendo credential rejected")
	}
	identity, err := deploymentAccountIdentity(pid)
	if err != nil {
		return 0, "", status.Error(codes.Unauthenticated, "account identity rejected")
	}
	return pid, nplnTenant + "/users/" + identity.UserID, nil
}

func authenticatedConsole(ext *authpb.ExternalIdToken, pid uint64) bool {
	if os.Getenv("NPLN_DEPLOYMENT") == "development" {
		return externalConsoleFriends(ext, pid)
	}
	return deploymentAccountVerifier != nil && ext != nil && deploymentAccountVerifier.DeviceKind(ext.GetNsaIdToken()) == "switch"
}
