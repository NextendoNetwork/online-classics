package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Development observation server: logs method names,
// never request contents or authentication tokens.
func handler(logger *log.Logger) http.Handler {
	return handlerWithSessionProfile(logger, "gss")
}

func handlerWithSessionProfile(logger *log.Logger, profile string) http.Handler {
	return handlerWithICE(logger, profile, iceEndpoint{})
}

func handlerWithICE(logger *log.Logger, profile string, ice iceEndpoint, relays ...*localTURN) http.Handler {
	return handlerWithIdentityDiagnostic(logger, profile, ice, nil, relays...)
}

func handlerWithIdentityDiagnostic(logger *log.Logger, profile string, ice iceEndpoint, diagnostic *identityDiagnostic, relays ...*localTURN) http.Handler {
	return handlerWithLocalFriends(logger, profile, ice, diagnostic, nil, relays...)
}

func handlerWithLocalFriends(logger *log.Logger, profile string, ice iceEndpoint, diagnostic *identityDiagnostic, pair *localFriendPair, relays ...*localTURN) http.Handler {
	return handlerWithQueryExperiment(logger, profile, ice, diagnostic, pair, false, relays...)
}

func handlerWithQueryExperiment(logger *log.Logger, profile string, ice iceEndpoint, diagnostic *identityDiagnostic, pair *localFriendPair, queryMembers bool, relays ...*localTURN) http.Handler {
	return handlerWithSessionExperiments(logger, profile, ice, diagnostic, pair, queryMembers, false, relays...)
}

func handlerWithSessionExperiments(logger *log.Logger, profile string, ice iceEndpoint, diagnostic *identityDiagnostic, pair *localFriendPair, queryMembers, stage bool, relays ...*localTURN) http.Handler {
	return handlerWithUserStateExperiment(logger, profile, ice, diagnostic, pair, queryMembers, stage, false, relays...)
}

func handlerWithUserStateExperiment(logger *log.Logger, profile string, ice iceEndpoint, diagnostic *identityDiagnostic, pair *localFriendPair, queryMembers, stage, userState bool, relays ...*localTURN) http.Handler {
	return handlerWithConfiguredAuth(nil, logger, profile, ice, diagnostic, pair, queryMembers, stage, userState, relays...)
}

func handlerWithConfiguredAuth(configured *labAuth, logger *log.Logger, profile string, ice iceEndpoint, diagnostic *identityDiagnostic, pair *localFriendPair, queryMembers, stage, userState bool, relays ...*localTURN) http.Handler {
	mux := http.NewServeMux()
	labRooms := newRoomStore()
	if configured == nil {
		mux.HandleFunc("/lab/rooms", labRooms.listOrCreate)
		mux.HandleFunc("/lab/rooms/", labRooms.join)
	}
	auth := configured
	if auth == nil {
		var err error
		auth, err = newLabAuth()
		if err != nil {
			panic(err)
		}
	}
	auth.sessionProfile = profile
	auth.identityDiagnostic = diagnostic
	auth.friendPair = pair
	auth.logger = logger
	if pair != nil || auth.nextendo != nil {
		auth.presences = newLocalPresenceStore()
		// Disabled: looping LoginDeviceToken delivery is unverified for Genesis;
		// the real-client test left both clients waiting.
	}
	mux.HandleFunc(listFriendUsersPath, auth.listLocalFriends)
	mux.HandleFunc("/jwkSets/nplnAccessToken", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		logger.Print("Local public-key query: jwkSets/nplnAccessToken")
		auth.publicKeySet(w, r)
	})
	tickets := newTicketStore()
	if auth.nextendo != nil {
		tickets.bounded = true
		auth.nextendo.rooms = tickets
	}
	tickets.genesisQueryMembers = queryMembers
	tickets.genesisStage = stage
	tickets.genesisUserState = userState
	mux.HandleFunc(queryGameSessionsPath, tickets.queryMatchSessions(auth, logger, labRooms))
	mux.HandleFunc(joinGameSessionPath, tickets.joinMatchSession(auth, logger))
	mux.HandleFunc(getGameSessionPath, tickets.getMatchSession(auth, logger))
	mux.HandleFunc(allocateIceServerSetPath, auth.allocateIceServerSet(ice, logger, relays...))
	mux.HandleFunc(gamesyncIssueTokenPath, tickets.issueGamesyncToken(auth, logger))
	mux.HandleFunc(gamesyncKeepUserSessionPath, tickets.keepGamesyncSession(logger))
	mux.HandleFunc(gamesyncGetDocumentPath, tickets.getGamesyncDocument(logger))
	mux.HandleFunc(gamesyncListDocumentsPath, tickets.listGamesyncDocuments(logger))
	mux.HandleFunc(gamesyncWriteDocumentsPath, tickets.inspectGamesyncWrites(logger))
	mux.HandleFunc(issuePrearrangedUserTokenPath, auth.issuePrearrangedUserToken)
	if auth.nextendo != nil {
		mux.HandleFunc(refreshTokenPath, auth.refreshAccountToken)
	}
	mux.HandleFunc(activateUserPath, auth.activateUser)
	mux.HandleFunc(subscribeMaintenancePath, subscribeMaintenance)
	mux.HandleFunc(subscribeFriendsPath, auth.subscribeFriends)
	mux.HandleFunc(subscribePresencesPath, auth.subscribePresences)
	mux.HandleFunc(presenceKeepAlivePath, auth.keepPresenceAlive)
	mux.HandleFunc(recvMessagePath, auth.recvMessage)
	mux.HandleFunc(sendMessagePath, auth.inspectSendMessage(logger))
	mux.HandleFunc(createGameSessionCreationTicketPath, tickets.createGameSessionCreationTicket(auth, logger))
	mux.HandleFunc(trackGameSessionCreationTicketPath, tickets.trackGameSessionCreationTicket(auth, logger))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "project": "genesis-lab"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			http.NotFound(w, r)
			return
		}

		// Read at most 1 MiB to close the request correctly. Never retain it.
		_, err := io.Copy(io.Discard, io.LimitReader(r.Body, 1<<20+1))
		if err != nil {
			logger.Printf("Read failed: method=%q error=%v", r.URL.Path, err)
		}
		// gRPC status 12 = UNIMPLEMENTED. An explicit response is more useful
		// for diagnosis than fabricated success or a permanently loading UI.
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Grpc-Status", "12")
		w.Header().Set("Grpc-Message", "genesis-lab: method not implemented")
	})
	var presenceLogs presenceLogLimiter
	result := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logRequest := true
		if r.URL.Path == subscribePresencesPath {
			var skipped int
			logRequest, skipped = presenceLogs.allow(r.RemoteAddr)
			if logRequest && skipped > 0 {
				logger.Printf("Presence log summary: client=%q omitted_retries=%d", r.RemoteAddr, skipped)
			}
		}
		if logRequest && r.Method == http.MethodPost && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			// Log metadata only. Never print authorization or the body.
			logger.Printf("gRPC call: method=%q protocol=%s declared_bytes=%d tenant=%q",
				r.URL.Path, r.Proto, r.ContentLength, r.Header.Get("npln-tenant-id"))
		}
		mux.ServeHTTP(w, r)
		if logRequest && r.Method == http.MethodPost && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			if status := w.Header().Get("Grpc-Status"); status != "" {
				logger.Printf("reply gRPC: method=%q state=%s", r.URL.Path, status)
			} else {
				logger.Printf("Stream ended: method=%q", r.URL.Path)
			}
		}
	})
	if auth.nextendo != nil {
		return auth.enforceGamesyncAccounts(result, tickets)
	}
	return result
}

func developmentCertificate() (tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "genesis-lab localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", "t-7b4e32ca-lp1.lp1.t.npln.srv.nintendo.net", "gamesync.npln.nintendo.net"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	if addresses, err := net.InterfaceAddrs(); err == nil {
		for _, address := range addresses {
			if subnet, ok := address.(*net.IPNet); ok && subnet.IP.To4() != nil && subnet.IP.IsPrivate() {
				template.IPAddresses = append(template.IPAddresses, subnet.IP)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	)
}

func main() {
	deployment := flag.String("deployment", "nextendo", "nextendo or explicit development")
	config := flag.String("config", "", "Private Nextendo deployment JSON")
	addr := flag.String("addr", "127.0.0.1:8443", "Local listen address for the lab")
	logPath := flag.String("log-file", "genesis-lab.log", "Diagnostic file (empty for console output only)")
	traceH2 := flag.Bool("trace-h2", false, "Log HTTP/2 metadata without bodies or tokens; requires Go 1.27")
	sessionProfile := flag.String("session-token-profile", "gss", "Experimental session token profile: gss or npln-gss")
	stunAddr := flag.String("stun-addr", "127.0.0.1:3478", "STUN UDP on loopback or an assigned private IPv4; empty to disable")
	turnAddr := flag.String("turn-addr", "127.0.0.1:3479", "TURN UDP on loopback or an assigned private IPv4; empty to disable")
	udpLoopbackDiagnostic := flag.Bool("udp-diagnostic-loopback", false, "Log UDP without replying on 127.0.0.1 and 127.0.0.2 STUN/TURN ports; requires LAN listeners")
	udpSessionDiagnostic := flag.String("udp-diagnostic-session", "", "Experiment: nonreplying UDP listeners on comma-separated local IPv4 addresses; may change port-unreachable ICMP")
	identityPath := flag.String("identity-diagnostic-file", "", "Optional JSONL with IPs and IDs for lab friend mapping; no tokens")
	friendsClients := flag.String("lab-friend-clients", "", "Experimental local pair: two comma-separated source IPs; one profile per client")
	friendsNSA := flag.String("lab-friend-nsa", "", "NSA IDs for both clients: IP=16hex,IP=16hex; requires a local pair")
	queryMembers := flag.Bool("lab-genesis-query-members", false, "Experiment: include participants in Genesis Query BASIC; requires a local pair")
	stage := flag.Bool("lab-genesis-stage", false, "Experiment: publish __stg/All for Genesis LCLA6-2P; requires a local pair")
	userState := flag.Bool("lab-genesis-user-state", false, "Experiment: seed __stu with actual suid/susid and empty pl; requires a local pair")
	flag.Parse()
	if *deployment == "nextendo" {
		invalid := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name != "deployment" && f.Name != "config" {
				invalid = true
			}
		})
		if invalid {
			log.Fatal("Nextendo mode accepts only deployment and config flags")
		}
		if err := runNextendoDeployment(*config); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *deployment != "development" {
		log.Fatal("unknown deployment mode")
	}

	logger := log.New(log.Writer(), "genesis-lab: ", log.LstdFlags|log.Lmicroseconds)
	pair, err := newLocalFriendPair(*friendsClients)
	if err != nil {
		logger.Fatal(err)
	}
	if err := pair.setNSAOverrides(*friendsNSA); err != nil {
		logger.Fatal(err)
	}
	if *queryMembers && pair == nil {
		logger.Fatal("lab-genesis-query-members requires lab-friend-clients")
	}
	if *stage && pair == nil {
		logger.Fatal("lab-genesis-stage requires lab-friend-clients")
	}
	if *userState && pair == nil {
		logger.Fatal("lab-genesis-user-state requires lab-friend-clients")
	}
	if *stage {
		logger.Print("Genesis __stg/All experiment enabled; schema awaits real-client validation")
	}
	var diagnostic *identityDiagnostic
	if *identityPath != "" {
		file, err := os.OpenFile(*identityPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			logger.Fatal("Could not open the identity diagnostic")
		}
		defer file.Close()
		diagnostic = &identityDiagnostic{out: file}
		logger.Print("Identity diagnostic enabled: IPs and user IDs; no tokens")
	}
	if *sessionProfile != "gss" && *sessionProfile != "npln-gss" {
		logger.Fatal("session-token-profile must be gss or npln-gss")
	}
	if *logPath != "" {
		file, err := os.OpenFile(*logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			logger.Fatal(err)
		}
		defer file.Close()
		logger.SetOutput(io.MultiWriter(log.Writer(), file))
		logger.Printf("Test started; log=%q", *logPath)
	}
	if pair != nil {
		logger.Print("Experimental local pair enabled for two clients; stable local identities and Friends active")
		logger.Printf("Friends local: NSA configured=%t", *friendsNSA != "")
		logger.Printf("Experimental Genesis query: include_BASIC_participants=%t", *queryMembers)
		logger.Printf("Experimental Genesis state: stu_seed=%t partial_schema=true", *userState)
	}
	cert, err := developmentCertificate()
	if err != nil {
		logger.Fatal(err)
	}
	var ice iceEndpoint
	var relay *localTURN
	if *stunAddr != "" {
		stun, endpoint, err := startLocalSTUN(*stunAddr, logger)
		if err != nil {
			logger.Fatal(err)
		}
		defer stun.Close()
		ice = endpoint
		logger.Printf("Local STUN listening on %s", stun.LocalAddr())
	}
	if *turnAddr != "" {
		if *stunAddr == "" {
			logger.Fatal("Local TURN requires STUN to be enabled")
		}
		relay, err = startLocalTURN(*turnAddr, logger)
		if err != nil {
			logger.Fatal(err)
		}
		defer relay.Close()
		logger.Printf("TURN listening on %s:%d; peers restricted to the adapter subnet", relay.endpoint.host, relay.endpoint.port)
	}
	if *udpLoopbackDiagnostic {
		if ice.host == "" || relay == nil || net.ParseIP(ice.host).IsLoopback() || net.ParseIP(relay.endpoint.host).IsLoopback() {
			logger.Fatal("udp-diagnostic-loopback requires STUN and TURN on the LAN IP")
		}
		diagnosticUDP, err := startLoopbackUDPDiagnostics([]int{ice.port, relay.endpoint.port}, logger)
		if err != nil {
			logger.Fatal(err)
		}
		defer closeUDPDiagnostics(diagnosticUDP)
	}
	if *udpSessionDiagnostic != "" {
		addresses := strings.Split(*udpSessionDiagnostic, ",")
		for i := range addresses {
			addresses[i] = strings.TrimSpace(addresses[i])
		}
		diagnosticUDP, err := startSessionUDPDiagnostics(addresses, logger)
		if err != nil {
			logger.Fatal(err)
		}
		defer closeUDPDiagnostics(diagnosticUDP)
		logger.Print("UDP session diagnostic active: no replies or payloads; opening the port may change ICMP")
	}
	observer := newTransportObserver(logger)
	server := &http.Server{
		Addr:              *addr,
		Handler:           observer.wrap(handlerWithUserStateExperiment(logger, *sessionProfile, ice, diagnostic, pair, *queryMembers, *stage, *userState, relay)),
		ConnContext:       observer.context,
		ConnState:         observer.state,
		ErrorLog:          logger,
		ReadHeaderTimeout: 5 * time.Second,
		TLSConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"h2", "http/1.1"},
			Certificates: []tls.Certificate{cert},
			GetConfigForClient: func(info *tls.ClientHelloInfo) (*tls.Config, error) {
				logger.Printf("tls: ClientHello sni=%q", info.ServerName)
				return nil, nil // Preserve the current certificate and TLS policy.
			},
		},
	}
	logger.Printf("Listening at https://%s/healthz", *addr)
	logger.Printf("Experimental session token profile: %s", *sessionProfile)
	logger.Print("Temporary certificate for local tests only; not installed on Switch or Ryujinx")
	logger.Print("Local certificate: SAN includes NPLN and gamesync.npln.nintendo.net")
	if *traceH2 {
		// Go 1.27 recognizes HandshakeContext/ConnectionState on the TLS wrapper.
		server.Protocols = new(http.Protocols)
		server.Protocols.SetHTTP1(true)
		server.Protocols.SetHTTP2(true)
		listener, err := net.Listen("tcp", *addr)
		if err != nil {
			logger.Fatal(err)
		}
		logger.Print("HTTP/2 diagnostic active: frame headers and codes only; no payloads")
		logger.Fatal(server.Serve(&tracedTLSListener{Listener: listener, config: server.TLSConfig, logger: logger}))
		return
	}
	if err := server.ListenAndServeTLS("", ""); err != nil {
		logger.Fatal(fmt.Errorf("Server stopped: %w", err))
	}
}
