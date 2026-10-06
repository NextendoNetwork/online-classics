package main

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"log"
	"net"
	"net/http/httptest"
	"testing"
	"time"
)

func bindingRequest() []byte {
	b := make([]byte, 20)
	binary.BigEndian.PutUint16(b, 1)
	binary.BigEndian.PutUint32(b[4:], stunCookie)
	copy(b[8:], []byte("transaction1"))
	return b
}

func TestLocalSTUNRealUDP(t *testing.T) {
	s, endpoint, err := startLocalSTUN("127.0.0.1:0", log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, err := net.DialUDP("udp", nil, s.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if endpoint.port != s.LocalAddr().(*net.UDPAddr).Port {
		t.Fatal("Announcement does not match listener")
	}
	c.SetDeadline(time.Now().Add(time.Second))
	request := bindingRequest()
	if _, err = c.Write(request); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 512)
	n, err := c.Read(b)
	if err != nil {
		t.Fatal(err)
	}
	b = b[:n]
	if n != 40 || binary.BigEndian.Uint16(b) != 0x0101 || !bytes.Equal(b[4:20], request[4:20]) {
		t.Fatalf("Invalid STUN reply: %x", b)
	}
	if got := int(binary.BigEndian.Uint16(b[26:28]) ^ uint16(stunCookie>>16)); got != c.LocalAddr().(*net.UDPAddr).Port {
		t.Fatal("Incorrect XOR port")
	}
	ip := make(net.IP, 4)
	for i := range ip {
		ip[i] = b[28+i] ^ request[4+i]
	}
	if !ip.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatal("Incorrect XOR address")
	}
	if binary.BigEndian.Uint32(b[36:]) != crc32.ChecksumIEEE(b[:32])^0x5354554e {
		t.Fatal("Incorrect fingerprint")
	}
}

func TestSTUNRejectsInvalidRequestsAndIPv6Mapping(t *testing.T) {
	peer := &net.UDPAddr{IP: net.ParseIP("::1"), Port: 43210}
	valid := bindingRequest()
	response := stunBindingResponse(valid, peer)
	if len(response) != 52 || response[25] != 2 {
		t.Fatal("Missing IPv6 XOR")
	}
	for i := 0; i < 16; i++ {
		if response[28+i]^valid[4+i] != peer.IP.To16()[i] {
			t.Fatal("Incorrect IPv6 XOR")
		}
	}
	for _, req := range [][]byte{valid[:19], append(append([]byte{}, valid...), 0), append([]byte{0, 3}, valid[2:]...)} {
		if stunBindingResponse(req, peer) != nil {
			t.Fatal("Accepted invalid frame")
		}
	}
	if c, _, err := startLocalSTUN("0.0.0.0:0", quiet()); err == nil {
		c.Close()
		t.Fatal("Accepted nonlocal address")
	}
}

func TestICEAuthorizationAndEndpoint(t *testing.T) {
	a, err := newLabAuth()
	if err != nil {
		t.Fatal(err)
	}
	claims := labClaims{Subject: "u-test", Expires: time.Now().Add(time.Hour).Unix()}
	claims.NPLN.Tenant = labTenant
	token, err := a.signJWT(claims)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tenant, user, token, status string
		endpoint                    iceEndpoint
	}{
		{"tenants/current", "", token, "0", iceEndpoint{"127.0.0.1", 3478}},
		{"tenants/current", "tenants/current/users/current", token, "0", iceEndpoint{"127.0.0.1", 3478}},
		{"tenants/current", "tenants/" + labTenant + "/users/other", token, "7", iceEndpoint{"127.0.0.1", 3478}},
		{"tenants/other", "", token, "3", iceEndpoint{"127.0.0.1", 3478}},
		{"tenants/current", "", "forged", "16", iceEndpoint{"127.0.0.1", 3478}},
		{"tenants/current", "", token, "9", iceEndpoint{}},
	} {
		payload := protoBytes(nil, 1, []byte(tc.tenant))
		payload = protoBytes(payload, 2, []byte(tc.user))
		frame := httptest.NewRecorder()
		writeGRPCFrame(frame, payload)
		req := httptest.NewRequest("POST", allocateIceServerSetPath, bytes.NewReader(frame.Body.Bytes()))
		req.Header.Set("Content-Type", "application/grpc")
		req.Header.Set("npln-tenant-id", labTenant)
		req.Header.Set("Authorization", "Bearer "+tc.token)
		rr := httptest.NewRecorder()
		a.allocateIceServerSet(tc.endpoint, quiet())(rr, req)
		if got := rr.Result().Trailer.Get("Grpc-Status"); got != tc.status {
			t.Fatalf("ICE %s: %s != %s", tc.user, got, tc.status)
		}
		if tc.status == "0" {
			m := decodeTestMessage(t, rr.Body.Bytes()[5:])
			ttl, present := m[4]
			if !present || len(ttl) != 0 || string(m[1]) != "tenants/"+labTenant+"/iceServerSets/static" || len(m[5]) == 0 || len(m[6]) == 0 {
				t.Fatal("ICE metadata shape differs from reference")
			}
			stun := decodeTestMessage(t, m[2])
			if string(stun[1]) != "127.0.0.1" || !bytes.Equal(stun[3], []byte{1}) || m[3] != nil {
				t.Fatal("ICE announces incorrect endpoint or nonexistent TURN")
			}
		}
	}
}
