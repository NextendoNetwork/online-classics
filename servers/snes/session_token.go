package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Typed common.MapValue JSON representation (not flattened to ordinary
// values). Preserves int64 as strings, following protobuf JSON.
func attributeMap(data []byte, depth int) (map[string]any, error) {
	if depth > 32 {
		return nil, errors.New("Attributes nested too deeply")
	}
	fields := map[string]any{}
	err := visitProto(data, func(field, wire, _ uint64, entry []byte) error {
		if field != 1 {
			return nil
		}
		if wire != 2 {
			return errors.New("Invalid map entry")
		}
		var key string
		var value = map[string]any{}
		err := visitProto(entry, func(f, w, _ uint64, v []byte) error {
			if f == 1 || f == 2 {
				if w != 2 {
					return errors.New("Invalid map field")
				}
			}
			if f == 1 {
				key = string(v)
			}
			if f == 2 {
				var err error
				value, err = attributeValue(v, depth+1)
				return err
			}
			return nil
		})
		if err == nil {
			fields[key] = value
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return map[string]any{}, nil
	}
	return map[string]any{"fields": fields}, nil
}

func attributeValue(data []byte, depth int) (map[string]any, error) {
	if depth > 32 {
		return nil, errors.New("Attributes nested too deeply")
	}
	result := map[string]any{}
	err := visitProto(data, func(f, w, n uint64, v []byte) error {
		if f > 11 {
			return errors.New("Unknown attribute type")
		}
		want := uint64(2)
		if f <= 3 {
			want = 0
		}
		if f == 4 {
			want = 5
		}
		if f == 5 {
			want = 1
		}
		if w != want {
			return errors.New("Invalid attribute field type")
		}
		var key string
		var value any
		switch f {
		case 1:
			key, value = "nullValue", nil
		case 2:
			key, value = "booleanValue", n != 0
		case 3:
			key, value = "integerValue", strconv.FormatInt(int64(n), 10)
		case 4:
			key, value = "floatValue", math.Float32frombits(binary.LittleEndian.Uint32(v))
		case 5:
			key, value = "doubleValue", math.Float64frombits(binary.LittleEndian.Uint64(v))
		case 6:
			sec, nano, err := protoTimeParts(v)
			if err != nil || nano < 0 || nano >= 1e9 {
				return errors.New("Invalid timestamp")
			}
			key, value = "timestampValue", time.Unix(sec, int64(nano)).UTC().Format(time.RFC3339Nano)
		case 7:
			key, value = "stringValue", string(v)
		case 8:
			key, value = "bytesValue", base64.StdEncoding.EncodeToString(v)
		case 9:
			values := []any{}
			err := visitProto(v, func(af, aw, _ uint64, av []byte) error {
				if af != 1 {
					return nil
				}
				if aw != 2 {
					return errors.New("Invalid array")
				}
				item, err := attributeValue(av, depth+1)
				if err == nil {
					values = append(values, item)
				}
				return err
			})
			if err != nil {
				return err
			}
			key, value = "arrayValue", map[string]any{"values": values}
		case 10:
			m, err := attributeMap(v, depth+1)
			if err != nil {
				return err
			}
			key, value = "mapValue", m
		case 11:
			key, value = "referenceValue", string(v)
		}
		result = map[string]any{key: value}
		return nil
	})
	return result, err
}

func protoTimeParts(data []byte) (int64, int32, error) {
	var seconds int64
	var nanos int32
	err := visitProto(data, func(f, w, n uint64, _ []byte) error {
		if f == 1 || f == 2 {
			if w != 0 {
				return errors.New("Invalid time")
			}
			if f == 1 {
				seconds = int64(n)
			} else {
				nanos = int32(n)
			}
		}
		return nil
	})
	return seconds, nanos, err
}

func latencyJSON(data []byte) (map[string]any, error) {
	latencies := map[string]string{}
	err := visitProto(data, func(f, w, _ uint64, v []byte) error {
		if f != 1 {
			return nil
		}
		if w != 2 {
			return errors.New("Invalid latency")
		}
		var region string
		duration := "0s"
		err := visitProto(v, func(ef, ew, _ uint64, ev []byte) error {
			if ef == 1 || ef == 2 {
				if ew != 2 {
					return errors.New("Invalid latency entry")
				}
			}
			if ef == 1 {
				region = string(ev)
			}
			if ef == 2 {
				sec, nano, err := protoTimeParts(ev)
				if err != nil || sec < 0 || nano < 0 || nano >= 1e9 {
					return errors.New("Invalid duration")
				}
				duration = strconv.FormatInt(sec, 10)
				if nano != 0 {
					duration += "." + strings.TrimRight(fmt.Sprintf("%09d", nano), "0")
				}
				duration += "s"
			}
			return nil
		})
		if err == nil {
			latencies[region] = duration
		}
		return err
	})
	return map[string]any{"latencies": latencies}, err
}

// The gss JWT attr claim does not use protobuf JSON. Each value contains
// {"type":"...","value":...}, following the format observed by Nextendo.
func gamesyncAttributeMap(data []byte) (map[string]any, error) {
	result := map[string]any{}
	err := visitProto(data, func(field, wire, _ uint64, entry []byte) error {
		if field != 1 {
			return nil
		}
		if wire != 2 {
			return errors.New("Invalid attribute map")
		}
		var key string
		var value []byte
		if err := visitProto(entry, func(f, w, _ uint64, v []byte) error {
			if f == 1 || f == 2 {
				if w != 2 {
					return errors.New("Invalid attribute entry")
				}
				if f == 1 {
					key = string(v)
				} else {
					value = v
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if key == "" || value == nil {
			return errors.New("Incomplete attribute")
		}
		typed, err := gamesyncAttributeValue(value, 0)
		if err != nil {
			return err
		}
		result[key] = typed
		return nil
	})
	return result, err
}

func gamesyncAttributeValue(data []byte, depth int) (map[string]any, error) {
	if depth > 32 {
		return nil, errors.New("Attributes nested too deeply")
	}
	var result map[string]any
	err := visitProto(data, func(f, w, n uint64, v []byte) error {
		if result != nil {
			return errors.New("Value contains multiple types")
		}
		switch f {
		case 2:
			if w != 0 {
				return errors.New("Invalid boolean")
			}
			result = map[string]any{"type": "boolean", "value": n != 0}
		case 3:
			if w != 0 {
				return errors.New("Invalid integer")
			}
			result = map[string]any{"type": "integer", "value": int64(n)}
		case 4:
			if w != 5 {
				return errors.New("Invalid float")
			}
			result = map[string]any{"type": "double", "value": float64(math.Float32frombits(binary.LittleEndian.Uint32(v)))}
		case 5:
			if w != 1 {
				return errors.New("Invalid double")
			}
			result = map[string]any{"type": "double", "value": math.Float64frombits(binary.LittleEndian.Uint64(v))}
		case 7, 11:
			if w != 2 {
				return errors.New("Invalid string")
			}
			result = map[string]any{"type": "string", "value": string(v)}
		case 9:
			if w != 2 {
				return errors.New("Invalid array")
			}
			items := []any{}
			if err := visitProto(v, func(af, aw, _ uint64, av []byte) error {
				if af != 1 {
					return nil
				}
				if aw != 2 {
					return errors.New("Invalid item")
				}
				item, err := gamesyncAttributeValue(av, depth+1)
				if err == nil {
					items = append(items, item)
				}
				return err
			}); err != nil {
				return err
			}
			result = map[string]any{"type": "array", "value": items}
		default:
			return errors.New("Unsupported Gamesync type")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("Empty value")
	}
	return result, nil
}

func gamesyncLatencyJSON(data []byte) (map[string]any, error) {
	latencies := map[string]any{}
	err := visitProto(data, func(f, w, _ uint64, entry []byte) error {
		if f != 1 {
			return nil
		}
		if w != 2 {
			return errors.New("Invalid latency")
		}
		var region string
		var nanos int64
		if err := visitProto(entry, func(ef, ew, _ uint64, v []byte) error {
			if ef == 1 {
				if ew != 2 {
					return errors.New("Invalid region")
				}
				region = string(v)
			}
			if ef == 2 {
				if ew != 2 {
					return errors.New("Invalid duration")
				}
				sec, ns, err := protoTimeParts(v)
				if err != nil || sec < 0 || ns < 0 || ns >= 1e9 || sec > math.MaxInt64/1_000_000_000 {
					return errors.New("Invalid duration")
				}
				nanos = sec*1e9 + int64(ns)
			}
			return nil
		}); err != nil {
			return err
		}
		if region == "" {
			return errors.New("Empty region")
		}
		latencies[region] = map[string]any{"nanos": nanos}
		return nil
	})
	return map[string]any{"latencies": latencies}, err
}

func (a *labAuth) sessionToken(ticket pendingTicket, now time.Time) (string, error) {
	if a.sessionProfile == "npln-gss" {
		return a.nplnSessionToken(ticket, now)
	}
	attrs := map[string]any{}
	latencies := map[string]any{"latencies": map[string]string{}}
	team := ""
	err := visitProto(ticket.user, func(f, w, _ uint64, v []byte) error {
		if f < 2 || f > 4 {
			return nil
		}
		if w != 2 {
			return errors.New("Invalid user definition")
		}
		var err error
		switch f {
		case 2:
			attrs, err = gamesyncAttributeMap(v)
		case 3:
			latencies, err = gamesyncLatencyJSON(v)
		case 4:
			team = string(v)
		}
		return err
	})
	if err != nil {
		return "", err
	}
	attrJSON, err := json.Marshal(attrs)
	if err != nil {
		return "", err
	}
	ltcyJSON, err := json.Marshal(latencies)
	if err != nil {
		return "", err
	}
	claims := map[string]any{
		"iss": "gss", "sub": ticket.owner, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
		"gamesync": map[string]any{
			"gsid": ticket.sessionName[strings.LastIndexByte(ticket.sessionName, '/')+1:],
			"usid": ticket.userName[strings.LastIndexByte(ticket.userName, '/')+1:],
			"uid":  ticket.owner, "tid": labTenant, "typ": 1,
			"attr": string(attrJSON), "ltcy": string(ltcyJSON), "team": team,
		},
	}
	// gss profile observed in Nextendo's public S3 reference.
	// Genesis acceptance still requires a real-client test for this profile.
	return a.signJWTHeader(claims, map[string]string{"alg": "ES256", "kid": "genesis-lab"})
}

// Alternative Nextendo profile used outside S3. Genesis acceptance is unverified;
// select it only for a comparative experiment.
func (a *labAuth) nplnSessionToken(ticket pendingTicket, now time.Time) (string, error) {
	claims := map[string]any{
		"iss": "default iss", "sub": ticket.owner, "iat": now.Unix(), "exp": now.Add(8 * time.Hour).Unix(),
		"npln": map[string]any{
			"tid": labTenant, "aid": "a-" + strings.TrimPrefix(ticket.owner, "u-"),
			"app_id": "01008d300c50c000", "ext_id_type": 1,
			"authorization": map[string]any{"allow": []string{"**"}, "deny": []string{}, "nso_restricted": false},
		},
		"gss": map[string]any{"game_session": ticket.sessionName, "user_session": ticket.userName},
	}
	return a.signJWTHeader(claims, map[string]string{
		"alg": "ES256", "kid": "genesis-lab",
		"jku": "https://" + labTenant + ".lp1.t.npln.srv.nintendo.net/jwkSets/nplnAccessToken",
	})
}

// Publishes only the public key of ephemeral lab tokens.
func (a *labAuth) publicKeySet(w http.ResponseWriter, _ *http.Request) {
	x, y := make([]byte, 32), make([]byte, 32)
	a.key.X.FillBytes(x)
	a.key.Y.FillBytes(y)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{
		"kty": "EC", "crv": "P-256", "alg": "ES256", "use": "sig", "kid": "genesis-lab",
		"x": base64.RawURLEncoding.EncodeToString(x), "y": base64.RawURLEncoding.EncodeToString(y),
	}}})
}
