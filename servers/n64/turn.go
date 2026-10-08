package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"sync"

	"github.com/pion/turn/v4"
)

const (
	defaultTURNListen   = "127.0.0.1:3479"
	defaultTURNHost     = "127.0.0.1"
	defaultTURNRealm    = "nextendo.local"
	defaultTURNUsername = "nextendo"
	defaultTURNPassword = "wonder-local"
)

type turnServer struct {
	accounts *turnAccountRegistry
	server   *turn.Server
	address  net.Addr
	once     sync.Once
}

func startTURN(recorder *probeRecorder, listenAddress, relayAddress, username, password, realm string) (*turnServer, error) {
	listen, err := net.ResolveUDPAddr("udp4", listenAddress)
	if err != nil {
		return nil, fmt.Errorf("resolve TURN listener: %w", err)
	}
	relayIP := net.ParseIP(relayAddress).To4()
	if relayIP == nil {
		return nil, fmt.Errorf("TURN relay address %q is not IPv4", relayAddress)
	}
	if username == "" || password == "" || realm == "" {
		return nil, fmt.Errorf("TURN username, password, and realm must be non-empty")
	}

	conn, err := net.ListenUDP("udp4", listen)
	if err != nil {
		return nil, fmt.Errorf("listen TURN UDP: %w", err)
	}

	key := turn.GenerateAuthKey(username, realm, password)
	production := os.Getenv("NPLN_DEPLOYMENT") != "development"
	state := &turnServer{accounts: &turnAccountRegistry{credentials: map[string]turnAccountCredential{}}}
	var generator turn.RelayAddressGenerator = &turn.RelayAddressGeneratorStatic{RelayAddress: relayIP, Address: relayIP.String()}
	permission := func(_ net.Addr, peer net.IP) bool { return true }
	if production {
		generator, err = productionRelayGenerator(relayIP)
		if err != nil {
			conn.Close()
			return nil, err
		}
		permission = func(_ net.Addr, peer net.IP) bool { return publicDeploymentIPv4(peer.String()) }
	}
	server, err := turn.NewServer(turn.ServerConfig{
		Realm: realm,
		AuthHandler: func(candidate, candidateRealm string, source net.Addr) ([]byte, bool) {
			if production {
				return state.accounts.authenticate(candidate, candidateRealm, realm, source)
			}
			return key, candidate == username && candidateRealm == realm
		},
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn:            conn,
			RelayAddressGenerator: generator,
			PermissionHandler:     permission,
		}},
		EventHandler: turn.EventHandler{
			OnAllocationCreated: func(src, dst net.Addr, protocol, eventUser, eventRealm string, relay net.Addr, requestedPort int) {
				if production {
					log.Print("[NPLN TURN] account allocation created")
					return
				}
				log.Printf("[NPLN TURN] allocation created src=%s relay=%s protocol=%s user=%q requested_port=%d", src, relay, protocol, eventUser, requestedPort)
				if recorder != nil {
					recorder.record(probeEvent{Kind: "turn_allocation_created", Fields: map[string]interface{}{
						"source": src.String(), "destination": dst.String(), "relay": relay.String(),
						"protocol": protocol, "username": eventUser, "realm": eventRealm, "requested_port": requestedPort,
					}})
				}
			},
			OnAllocationDeleted: func(src, dst net.Addr, protocol, eventUser, eventRealm string) {
				if production {
					log.Print("[NPLN TURN] account allocation deleted")
					return
				}
				log.Printf("[NPLN TURN] allocation deleted src=%s protocol=%s user=%q", src, protocol, eventUser)
				if recorder != nil {
					recorder.record(probeEvent{Kind: "turn_allocation_deleted", Fields: map[string]interface{}{
						"source": src.String(), "destination": dst.String(), "protocol": protocol,
						"username": eventUser, "realm": eventRealm,
					}})
				}
			},
		},
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("start TURN server: %w", err)
	}

	log.Printf("[NPLN TURN] RFC 8656 UDP listening on %s relay_ip=%s realm=%q user=%q", conn.LocalAddr(), relayIP, realm, username)
	state.server, state.address = server, conn.LocalAddr()
	return state, nil
}

func (s *turnServer) Close() {
	if s == nil || s.server == nil {
		return
	}
	s.once.Do(func() {
		if err := s.server.Close(); err != nil {
			log.Printf("[NPLN TURN] close: %v", err)
		}
	})
}
