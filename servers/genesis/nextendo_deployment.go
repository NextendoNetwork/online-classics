package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/NextendoNetwork/online-classics/accountauth"
	"github.com/pion/logging"
	"github.com/pion/turn/v5"
)

type nextendoDeployment struct {
	StatsListen          string                     `json:"statsListen"`
	StatsKeyEnv          string                     `json:"statsKeyEnv"`
	Listen               string                     `json:"listen"`
	TLSCertificate       string                     `json:"tlsCertificate"`
	TLSKey               string                     `json:"tlsKey"`
	SigningKey           string                     `json:"signingKey"`
	AdvertisedIPv4       string                     `json:"advertisedIPv4"`
	UDPBindIPv4          string                     `json:"udpBindIPv4"`
	STUNPort             int                        `json:"stunPort"`
	TURNPort             int                        `json:"turnPort"`
	RelayMinPort         uint16                     `json:"relayMinPort"`
	RelayMaxPort         uint16                     `json:"relayMaxPort"`
	IsolatedLoopbackTest bool                       `json:"isolatedLoopbackTest"`
	Account              accountauth.NextendoConfig `json:"account"`
}

func loadNextendoDeployment(path string) (nextendoDeployment, *labAuth, error) {
	var cfg nextendoDeployment
	file, err := os.Open(path)
	if err != nil {
		return cfg, nil, errors.New("private deployment config unavailable")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 65537))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&cfg); err != nil {
		return cfg, nil, errors.New("invalid deployment config")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return cfg, nil, errors.New("extra deployment data")
	}
	statsHost, statsPort, statsErr := net.SplitHostPort(cfg.StatsListen)
	statsIP := net.ParseIP(statsHost)
	statsNumber, statsParseErr := strconv.Atoi(statsPort)
	if statsErr != nil || statsIP == nil || !statsIP.IsLoopback() || statsNumber < 1 || statsNumber > 65535 || statsParseErr != nil || len(os.Getenv(cfg.StatsKeyEnv)) < 32 {
		return cfg, nil, errors.New("loopback presence endpoint and private stats key required")
	}
	host, port, err := net.SplitHostPort(cfg.Listen)
	listenIP := net.ParseIP(host)
	listenPort, e := strconv.Atoi(port)
	publicIP, bindIP := net.ParseIP(cfg.AdvertisedIPv4), net.ParseIP(cfg.UDPBindIPv4)
	if err != nil || e != nil || listenPort < 1 || listenPort > 65535 || listenIP == nil || listenIP.To4() == nil || publicIP == nil || publicIP.To4() == nil || bindIP == nil || bindIP.To4() == nil {
		return cfg, nil, errors.New("explicit IPv4 listeners and advertisement required")
	}
	if cfg.IsolatedLoopbackTest {
		if !listenIP.IsLoopback() || !publicIP.IsLoopback() || !bindIP.IsLoopback() {
			return cfg, nil, errors.New("isolated mode requires loopback listeners")
		}
	} else if !publicPeerIP(publicIP) {
		return cfg, nil, errors.New("deployment requires a public advertised IPv4")
	}
	if cfg.STUNPort < 1 || cfg.STUNPort > 65535 || cfg.TURNPort < 1 || cfg.TURNPort > 65535 || cfg.STUNPort == cfg.TURNPort || cfg.RelayMinPort < 1024 || cfg.RelayMaxPort < cfg.RelayMinPort || int(cfg.RelayMaxPort)-int(cfg.RelayMinPort) > 255 {
		return cfg, nil, errors.New("distinct UDP ports and bounded relay range required")
	}
	for _, p := range []int{cfg.STUNPort, cfg.TURNPort} {
		if p >= int(cfg.RelayMinPort) && p <= int(cfg.RelayMaxPort) {
			return cfg, nil, errors.New("listener overlaps relay port range")
		}
	}
	if cfg.Account.TitleID != nextendoTitleID || !cfg.Account.AllowAllAccounts {
		return cfg, nil, errors.New("exact game scope and open account enrollment required")
	}
	verifier, err := accountauth.NewNextendoAuth(cfg.Account, nil)
	if err != nil {
		return cfg, nil, err
	}
	identity, err := accountauth.IdentityClient(cfg.Account)
	if err != nil {
		return cfg, nil, err
	}
	raw, err := os.ReadFile(cfg.SigningKey)
	if err != nil || len(raw) > 16384 {
		return cfg, nil, errors.New("persistent signing key unavailable")
	}
	block, rest := pem.Decode(raw)
	if block == nil || len(rest) != 0 {
		return cfg, nil, errors.New("invalid signing key PEM")
	}
	var parsed any
	if block.Type == "EC PRIVATE KEY" {
		parsed, err = x509.ParseECPrivateKey(block.Bytes)
	} else if block.Type == "PRIVATE KEY" {
		parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	} else {
		err = errors.New("unsupported signing key")
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if err != nil || !ok || key.Curve != elliptic.P256() {
		return cfg, nil, errors.New("persistent P256 private key required")
	}
	if _, err = tls.LoadX509KeyPair(cfg.TLSCertificate, cfg.TLSKey); err != nil {
		return cfg, nil, errors.New("persistent TLS certificate unavailable")
	}
	auth := &labAuth{key: key, sessionProfile: "gss", nextendo: &nextendoSessions{presence: newClassicsPresence(), verifier: verifier, identity: identity, sessions: map[string]accountSession{}, refresh: map[[32]byte]string{}}}
	return cfg, auth, nil
}

// A public peer cannot use TURN to reach private services on the VPS network.
func publicPeerIP(ip net.IP) bool {
	v := ip.To4()
	if v == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if v[0] == 0 || v[0] >= 224 || (v[0] == 100 && v[1] >= 64 && v[1] <= 127) || (v[0] == 192 && v[1] == 0 && v[2] == 0) || (v[0] == 198 && (v[1] == 18 || v[1] == 19)) {
		return false
	}
	return true
}

func startNextendoSTUN(cfg nextendoDeployment) (*net.UDPConn, iceEndpoint, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(cfg.UDPBindIPv4), Port: cfg.STUNPort})
	if err != nil {
		return nil, iceEndpoint{}, err
	}
	go func() {
		buffer := make([]byte, 2048)
		count := 0
		window := time.Now()
		for {
			n, peer, e := conn.ReadFromUDP(buffer)
			if e != nil {
				return
			}
			now := time.Now()
			if now.Sub(window) >= time.Second {
				window = now
				count = 0
			}
			count++
			if count > 100 || (!publicPeerIP(peer.IP) && !(cfg.IsolatedLoopbackTest && peer.IP.IsLoopback())) {
				continue
			}
			response := stunBindingResponse(buffer[:n], peer)
			if response != nil {
				_, _ = conn.WriteToUDP(response, peer)
			}
		}
	}()
	return conn, iceEndpoint{cfg.AdvertisedIPv4, cfg.STUNPort}, nil
}

type issuedTURN struct {
	expires time.Time
	check   func(string) bool
}
type turnAccounts struct {
	mu     sync.Mutex
	issued map[string]issuedTURN
}

func (t *localTURN) credentialsForAccount(now time.Time, check func(string) bool) (string, string, error) {
	username, password, err := t.credentials(now)
	if err != nil {
		return "", "", err
	}
	t.accounts.mu.Lock()
	defer t.accounts.mu.Unlock()
	for k, v := range t.accounts.issued {
		if !v.expires.After(now) {
			delete(t.accounts.issued, k)
		}
	}
	if len(t.accounts.issued) >= 4096 {
		return "", "", errors.New("TURN credential capacity reached")
	}
	t.accounts.issued[username] = issuedTURN{now.Add(time.Hour), check}
	return username, password, nil
}

func startNextendoTURN(cfg nextendoDeployment) (*localTURN, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(cfg.UDPBindIPv4), Port: cfg.TURNPort})
	if err != nil {
		return nil, err
	}
	t := &localTURN{endpoint: iceEndpoint{cfg.AdvertisedIPv4, conn.LocalAddr().(*net.UDPAddr).Port}, accounts: &turnAccounts{issued: map[string]issuedTURN{}}}
	if _, err = rand.Read(t.secret[:]); err != nil {
		conn.Close()
		return nil, err
	}
	factory := logging.NewDefaultLoggerFactory()
	factory.Writer = io.Discard
	realm := "nextendo-classics"
	t.server, err = turn.NewServer(turn.ServerConfig{Realm: realm, LoggerFactory: factory,
		AuthHandler: func(r *turn.RequestAttributes) (string, []byte, bool) {
			peer, ok := r.SrcAddr.(*net.UDPAddr)
			if !ok || r.Realm != realm || !validTURNUsername(r.Username, time.Now()) {
				return "", nil, false
			}
			t.accounts.mu.Lock()
			issued, exists := t.accounts.issued[r.Username]
			t.accounts.mu.Unlock()
			if !exists || !issued.expires.After(time.Now()) || !issued.check(peer.IP.String()) {
				return "", nil, false
			}
			return r.Username, turn.GenerateAuthKey(r.Username, realm, t.password(r.Username)), true
		},
		PacketConnConfigs: []turn.PacketConnConfig{{PacketConn: conn, RelayAddressGenerator: &turn.RelayAddressGeneratorPortRange{RelayAddress: net.ParseIP(cfg.AdvertisedIPv4), Address: cfg.UDPBindIPv4, MinPort: cfg.RelayMinPort, MaxPort: cfg.RelayMaxPort, MaxRetries: 32}, PermissionHandler: func(_ net.Addr, peer net.IP) bool {
			return publicPeerIP(peer) || (cfg.IsolatedLoopbackTest && peer.IsLoopback())
		}}}})
	if err != nil {
		conn.Close()
		return nil, err
	}
	return t, nil
}

func runNextendoDeployment(path string) error {
	cfg, auth, err := loadNextendoDeployment(path)
	if err != nil {
		return err
	}
	logger := log.New(os.Stderr, "nextendo-classics: ", log.LstdFlags)
	auth.logger = logger
	statsListener, err := net.Listen("tcp4", cfg.StatsListen)
	if err != nil {
		return errors.New("private presence listener unavailable")
	}
	statsServer := &http.Server{Handler: auth.nextendo.presence.handler(os.Getenv(cfg.StatsKeyEnv)), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	defer statsServer.Close()
	go func() { _ = statsServer.Serve(statsListener) }()

	stun, endpoint, err := startNextendoSTUN(cfg)
	if err != nil {
		return fmt.Errorf("STUN listener unavailable: %w", err)
	}
	defer stun.Close()
	relay, err := startNextendoTURN(cfg)
	if err != nil {
		return fmt.Errorf("TURN listener unavailable: %w", err)
	}
	defer relay.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	handler := handlerWithConfiguredAuth(auth, logger, "gss", endpoint, nil, nil, false, false, false, relay)
	// Streams share this bounded request budget. The service supervisor also
	// applies MemoryMax/TasksMax; this is not a claim of completed load testing.
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				auth.nextendo.cleanup(now)
				auth.nextendo.rooms.pruneNextendo(now)
			}
		}
	}()
	slots := make(chan struct{}, 128)
	server := &http.Server{Addr: cfg.Listen, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 16384, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12}, BaseContext: func(net.Listener) context.Context { return ctx }, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "capacity reached", 503)
			return
		}
		if r.URL.Path != gamesyncKeepUserSessionPath && r.URL.Path != presenceKeepAlivePath {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		handler.ServeHTTP(w, r)
	})}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServeTLS(cfg.TLSCertificate, cfg.TLSKey) }()
	logger.Print("Nextendo mode active; persistent keys, account gate and authenticated bounded TURN")
	select {
	case err = <-done:
		if err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdown); err != nil {
			_ = server.Close()
		}
		<-done
	}
	return nil
}

const nextendoTitleID = "0100B3C014BDA000"
