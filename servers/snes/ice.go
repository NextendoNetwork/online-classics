package main

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"log"
	"net"
	"net/http"
	"time"
)

const allocateIceServerSetPath = "/nn.npln.matchmaking.v1.GameSessionService/AllocateIceServerSet"
const stunCookie uint32 = 0x2112a442

type iceEndpoint struct {
	host string
	port int
}

// Advertises only listeners actually open, with credentials accepted by
// the local TURN server when enabled.
func (a *labAuth) allocateIceServerSet(endpoint iceEndpoint, logger *log.Logger, relays ...*localTURN) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validGRPC(w, r) {
			return
		}
		uid, ok := a.authorizedUser(w, r)
		if !ok {
			return
		}
		payload, err := grpcRequestPayload(r)
		var tenant, user string
		if err == nil {
			err = visitProto(payload, func(f, wire, _ uint64, v []byte) error {
				if f == 1 || f == 2 {
					if wire != 2 {
						return errors.New("Invalid ICE field")
					}
					if f == 1 {
						tenant = string(v)
					} else {
						user = string(v)
					}
				}
				return nil
			})
		}
		if err != nil || (tenant != "tenants/current" && tenant != "tenants/"+labTenant) {
			grpcStatus(w, "3", "Invalid ICE request", nil)
			return
		}
		if user != "" && !validUserPath(user, uid) {
			grpcStatus(w, "7", "Unauthorized ICE user", nil)
			return
		}
		if endpoint.host == "" || endpoint.port <= 0 {
			grpcStatus(w, "9", "Local STUN disabled", nil)
			return
		}
		stun := protoBytes(nil, 1, []byte(endpoint.host))
		stun = protoVarint(stun, 2, uint64(endpoint.port))
		stun = protoVarint(stun, 3, 1)
		response := protoBytes(nil, 1, []byte("tenants/"+labTenant+"/iceServerSets/static"))
		response = protoBytes(response, 2, stun)
		turnCount := 0
		if len(relays) > 0 && relays[0] != nil {
			relay := relays[0]
			username, password, err := relay.credentials(time.Now())
			if err != nil {
				grpcStatus(w, "13", "Could not issue ICE credentials", nil)
				return
			}
			server := protoBytes(nil, 1, []byte(relay.endpoint.host))
			server = protoVarint(server, 2, uint64(relay.endpoint.port))
			server = protoVarint(server, 3, 1)
			server = protoBytes(server, 4, []byte(username))
			server = protoBytes(server, 5, []byte(password))
			response = protoBytes(response, 3, server)
			turnCount = 1
		}
		// Capture-compatible shape: Duration present and empty. TURN credentials
		// retain their independently validated one-hour expiration.
		response = protoBytes(response, 4, nil)
		response = protoBytes(response, 5, protoTimestamp(time.Now().UTC()))
		response = protoBytes(response, 6, protoVarint(nil, 1, 90))
		logger.Printf("AllocateIceServerSet: local STUN UDP available; TURN=%d", turnCount)
		grpcStatus(w, "0", "", response)
	}
}

func startLocalSTUN(address string, logger *log.Logger) (*net.UDPConn, iceEndpoint, error) {
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, iceEndpoint{}, err
	}
	subnet, err := labListenerNetwork(addr.IP)
	if err != nil {
		return nil, iceEndpoint{}, err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return nil, iceEndpoint{}, err
	}
	bound := conn.LocalAddr().(*net.UDPAddr)
	go func() {
		buffer := make([]byte, 2048)
		observed := 0
		for {
			n, peer, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			allowed := labPeerAllowed(subnet, peer.IP)
			logDatagram := observed < 16
			// Limited metadata only: no packet contents, credentials or peer IDs.
			if logDatagram {
				logger.Printf("STUN local: datagram received bytes=%d source_allowed=%t", n, allowed)
				observed++
			}
			if !allowed {
				continue
			}
			response := stunBindingResponse(buffer[:n], peer)
			if response == nil {
				if logDatagram {
					logger.Print("Local STUN: no reply to datagram; unsupported format or options")
				}
				continue
			}
			if _, err = conn.WriteToUDP(response, peer); err == nil {
				logger.Print("Local STUN: Binding response sent")
			}
		}
	}()
	return conn, iceEndpoint{bound.IP.String(), bound.Port}, nil
}

// RFC 8489: Binding, XOR-MAPPED-ADDRESS and FINGERPRINT; no TURN Allocate.
// Rejects truncated messages and other methods without reflecting their data.
func stunBindingResponse(request []byte, peer *net.UDPAddr) []byte {
	if len(request) < 20 || binary.BigEndian.Uint16(request) != 1 || binary.BigEndian.Uint32(request[4:8]) != stunCookie {
		return nil
	}
	n := int(binary.BigEndian.Uint16(request[2:4]))
	if n%4 != 0 || n != len(request)-20 {
		return nil
	}
	for pos := 20; pos < len(request); {
		if pos+4 > len(request) {
			return nil
		}
		kind := binary.BigEndian.Uint16(request[pos:])
		size := int(binary.BigEndian.Uint16(request[pos+2:]))
		end := pos + 4 + size
		if end > len(request) {
			return nil
		}
		if kind == 0x8028 {
			if size != 4 || end != len(request) || binary.BigEndian.Uint32(request[pos+4:]) != crc32.ChecksumIEEE(request[:pos])^0x5354554e {
				return nil
			}
		}
		// USERNAME/INTEGRITY require separate negotiation; do not simulate validation.
		if kind < 0x8000 {
			return nil
		}
		pos += 4 + (size+3)&^3
	}
	ip := peer.IP.To4()
	family := byte(1)
	if ip == nil {
		ip = peer.IP.To16()
		family = 2
	}
	if ip == nil {
		return nil
	}
	result := make([]byte, 20)
	binary.BigEndian.PutUint16(result, 0x0101)
	copy(result[4:20], request[4:20])
	value := make([]byte, 4+len(ip))
	value[1] = family
	binary.BigEndian.PutUint16(value[2:], uint16(peer.Port)^uint16(stunCookie>>16))
	for i, b := range ip {
		value[4+i] = b ^ request[4+i]
	}
	result = append(result, 0, 0x20, 0, byte(len(value)))
	result = append(result, value...)
	binary.BigEndian.PutUint16(result[2:], uint16(len(result)+8-20))
	fingerprint := crc32.ChecksumIEEE(result) ^ 0x5354554e
	result = append(result, 0x80, 0x28, 0, 4)
	result = binary.BigEndian.AppendUint32(result, fingerprint)
	return result
}
