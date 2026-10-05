package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

const issuePrearrangedUserTokenPath = "/nn.npln.auth.v1.Auth/IssuePrearrangedUserToken"

type labAuth struct {
	key                *ecdsa.PrivateKey
	sessionProfile     string
	identityDiagnostic *identityDiagnostic
	friendPair         *localFriendPair
	logger             *log.Logger
	presences          *localPresenceStore
	messages           *localMessageStore
	presenceLogMu      sync.Mutex
	presenceLogLast    map[string]time.Time
}

func newLabAuth() (*labAuth, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &labAuth{key: key, sessionProfile: "gss"}, nil
}

// Issues an ephemeral lab identity. The external token is used only
// to derive the user name; it is never printed or retained.
func (a *labAuth) issuePrearrangedUserToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
	if r.Header.Get("npln-tenant-id") != labTenant {
		grpcStatus(w, "3", "Incorrect tenant", nil)
		return
	}

	frame, err := io.ReadAll(io.LimitReader(r.Body, 64*1024+6))
	if err != nil || len(frame) < 5 || len(frame) > 64*1024+5 || frame[0] != 0 ||
		int(binary.BigEndian.Uint32(frame[1:5])) != len(frame)-5 {
		grpcStatus(w, "3", "Invalid gRPC frame", nil)
		return
	}
	request, err := parseAuthRequest(frame[5:])
	if err != nil || request.tenant != "tenants/"+labTenant || request.index > 15 || request.externalToken == "" {
		grpcStatus(w, "3", "Invalid authentication request", nil)
		return
	}

	peerKey, peer, paired := a.friendPair.peerFor(r)
	if paired {
		if request.index != 0 {
			grpcStatus(w, "3", "Local pair requires user_index zero", nil)
			return
		}
		request.localPeerIdentity = peer.uid
		request.localPeerNSA = peer.nsa
	}
	response, err := a.makeResponse(request)
	if err != nil {
		grpcStatus(w, "13", "Failed to issue local token", nil)
		return
	}
	if paired {
		a.friendPair.register(peerKey)
	}
	if a.identityDiagnostic != nil {
		var uid string
		_ = visitProto(response, func(f, w, _ uint64, v []byte) error {
			if f == 1 && w == 2 {
				return visitProto(v, func(f, w, _ uint64, v []byte) error {
					if f == 1 && w == 2 {
						uid = resourceLeaf(string(v))
					}
					return nil
				})
			}
			return nil
		})
		a.identityDiagnostic.record(r, "auth", uid, nil)
	}
	grpcStatus(w, "0", "", response)
}

type authRequest struct {
	tenant            string
	index             uint64
	externalToken     string
	externalType      uint64
	localPeerIdentity string // Set only by the server when the explicit test pair is enabled.
	localPeerNSA      string
}

func parseAuthRequest(data []byte) (authRequest, error) {
	var request authRequest
	err := visitProto(data, func(field, wire, number uint64, value []byte) error {
		switch field {
		case 1:
			if wire != 2 {
				return errors.New("Tenant is not a string")
			}
			request.tenant = string(value)
		case 2:
			if wire != 0 {
				return errors.New("Index is not an integer")
			}
			request.index = number
		case 3:
			if wire != 2 {
				return errors.New("External token is not a message")
			}
			return visitProto(value, func(innerField, innerWire, _ uint64, innerValue []byte) error {
				if innerField == 1 || innerField == 2 {
					if innerWire != 2 {
						return errors.New("External token is not a string")
					}
					request.externalToken = string(innerValue)
					request.externalType = innerField
				}
				return nil
			})
		}
		return nil
	})
	return request, err
}

// Walks protobuf fields without external dependencies. Unknown values
// are skipped so clients can send new fields.
func visitProto(data []byte, visit func(field, wire, number uint64, value []byte) error) error {
	for len(data) > 0 {
		tag, n := binary.Uvarint(data)
		if n <= 0 || tag>>3 == 0 {
			return errors.New("Invalid protobuf tag")
		}
		data = data[n:]
		field, wire := tag>>3, tag&7
		var number uint64
		var value []byte
		switch wire {
		case 0:
			number, n = binary.Uvarint(data)
			if n <= 0 {
				return errors.New("Invalid protobuf integer")
			}
			data = data[n:]
		case 1, 5:
			size := 8
			if wire == 5 {
				size = 4
			}
			if len(data) < size {
				return errors.New("Truncated protobuf field")
			}
			value, data = data[:size], data[size:]
		case 2:
			length, width := binary.Uvarint(data)
			if width <= 0 || length > uint64(len(data)-width) {
				return errors.New("Invalid protobuf length")
			}
			data = data[width:]
			value, data = data[:int(length)], data[int(length):]
		default:
			return errors.New("Unsupported protobuf type")
		}
		if err := visit(field, wire, number, value); err != nil {
			return err
		}
	}
	return nil
}

func (a *labAuth) makeResponse(request authRequest) ([]byte, error) {
	identity := sha256.Sum256(fmt.Appendf(nil, "%s:%d:%s", request.tenant, request.index, request.externalToken))
	label := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(identity[:13]))
	uid := "u-" + label
	account := "a-" + label
	extID := hex.EncodeToString(identity[:8])
	if request.localPeerIdentity != "" {
		uid = request.localPeerIdentity
		account = "a-" + strings.TrimPrefix(uid, "u-")
		extID = request.localPeerNSA
	}
	userName := "tenants/" + labTenant + "/users/" + uid

	now := time.Now()
	claims := map[string]any{
		"iss": "genesis-lab", "sub": uid, "iat": now.Unix(), "exp": now.Add(8 * time.Hour).Unix(),
		"npln": map[string]any{
			"tid": labTenant, "aid": account, "app_id": "01008d300c50c000",
			"ext_id": extID, "ext_id_type": request.externalType,
			"authorization":  map[string]any{"allow": []string{"**"}, "deny": []string{}},
			"nso_restricted": false,
		},
	}
	accessToken, err := a.signJWT(claims)
	if err != nil {
		return nil, err
	}
	refresh := make([]byte, 24)
	if _, err := rand.Read(refresh); err != nil {
		return nil, err
	}

	user := protoBytes(nil, 1, []byte(userName))
	user = protoBytes(user, 2, []byte("accounts/"+account))
	user = protoVarint(user, 5, binary.BigEndian.Uint64(identity[:8])&0x7fffffffffffffff)
	token := protoBytes(nil, 1, []byte(userName))
	token = protoBytes(token, 2, []byte(accessToken))
	token = protoBytes(token, 3, []byte(hex.EncodeToString(refresh)))
	token = protoBytes(token, 4, protoVarint(nil, 1, 8*60*60))
	response := protoBytes(nil, 1, user)
	return protoBytes(response, 2, token), nil
}

func (a *labAuth) signJWT(claims any) (string, error) {
	return a.signJWTHeader(claims, map[string]string{"alg": "ES256", "typ": "JWT", "kid": "genesis-lab"})
}

func (a *labAuth) signJWTHeader(claims any, metadata map[string]string) (string, error) {
	header, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, a.key, digest[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

type labClaims struct {
	Subject string `json:"sub"`
	Expires int64  `json:"exp"`
	NPLN    struct {
		Tenant string `json:"tid"`
	} `json:"npln"`
}

func (a *labAuth) verifyJWT(token string) (labClaims, bool) {
	var claims labClaims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return claims, false
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, false
	}
	var meta struct {
		Algorithm string `json:"alg"`
	}
	if json.Unmarshal(header, &meta) != nil || meta.Algorithm != "ES256" {
		return claims, false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) != 64 {
		return claims, false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&a.key.PublicKey, digest[:],
		new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])) {
		return claims, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || json.Unmarshal(payload, &claims) != nil {
		return claims, false
	}
	if claims.Subject == "" || claims.NPLN.Tenant != labTenant || claims.Expires <= time.Now().Unix() {
		return claims, false
	}
	return claims, true
}
