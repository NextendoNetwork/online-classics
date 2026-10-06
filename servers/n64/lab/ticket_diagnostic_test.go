package main

import (
	"bytes"
	"encoding/binary"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSummarizeCreationTicketIgnoresTokensAndPlayerAttributes(t *testing.T) {
	user := protoBytes(nil, 1, []byte("tenants/current/users/current"))
	user = protoBytes(user, 2, []byte("PRIVATE_ATTRIBUTES"))
	gameSession := protoVarint(nil, 2, 2)
	gameSession = protoBytes(gameSession, 11, []byte("PRIVATE_PROPERTIES"))
	ticket := protoBytes(nil, 2, []byte("tenants/current/matchmakingConfigs/genesis"))
	ticket = protoBytes(ticket, 3, user)
	ticket = protoBytes(ticket, 6, gameSession)
	payload := protoBytes(nil, 1, []byte("tenants/"+labTenant))
	payload = protoBytes(payload, 2, ticket)
	payload = protoBytes(payload, 3, []byte("PRIVATE_DELEGATION_TOKEN"))
	summary, err := summarizeCreationTicket(payload)
	if err != nil || !summary.parentOK || summary.users != 1 || summary.requestedMax != 2 || !summary.hasProperties || !summary.hasGameSession {
		t.Fatalf("Unexpected summary: %+v, error=%v", summary, err)
	}
	if summary.config != "tenants/current/matchmakingConfigs/genesis" {
		t.Fatalf("Incorrect configuration: %q", summary.config)
	}
}

func TestCreationTicketReturnsPendingWithoutLeakingToken(t *testing.T) {
	for _, profile := range []string{"gss", "npln-gss"} {
		t.Run(profile, func(t *testing.T) { testCreationTicketProfile(t, profile) })
	}
}

func testCreationTicketProfile(t *testing.T, profile string) {
	var output bytes.Buffer
	server := httptest.NewUnstartedServer(handlerWithSessionProfile(log.New(&output, "", 0), profile))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	authRequest := protoBytes(nil, 1, []byte("tenants/"+labTenant))
	authRequest = protoBytes(authRequest, 3, protoBytes(nil, 2, []byte("PRIVATE_EXTERNAL_TOKEN")))
	authResponse := callLabGRPC(t, server.Client(), server.URL+issuePrearrangedUserTokenPath, authRequest, "")
	if authResponse.status != "0" {
		t.Fatalf("Authentication gRPC %s", authResponse.status)
	}
	var token string
	_ = visitProto(authResponse.body, func(field, wire, _ uint64, value []byte) error {
		if field == 2 && wire == 2 {
			return visitProto(value, func(innerField, innerWire, _ uint64, innerValue []byte) error {
				if innerField == 2 && innerWire == 2 {
					token = string(innerValue)
				}
				return nil
			})
		}
		return nil
	})
	if token == "" {
		t.Fatal("Missing local token")
	}
	ticket := protoBytes(nil, 2, []byte("tenants/current/matchmakingConfigs/genesis"))
	ticket = protoBytes(ticket, 3, protoBytes(nil, 1, []byte("tenants/current/users/current")))
	payload := protoBytes(nil, 1, []byte("tenants/current"))
	payload = protoBytes(payload, 2, ticket)
	payload = protoBytes(payload, 3, []byte("PRIVATE_DELEGATION_TOKEN"))
	answer := callLabGRPC(t, server.Client(), server.URL+createGameSessionCreationTicketPath, payload, token)
	if answer.status != "0" {
		t.Fatalf("Expected pending ticket with gRPC 0: %s", answer.status)
	}
	var name, config, userName string
	var state uint64
	err := visitProto(answer.body, func(field, wire, number uint64, value []byte) error {
		switch field {
		case 1:
			name = string(value)
		case 2:
			config = string(value)
		case 3:
			return visitProto(value, func(userField, userWire, _ uint64, userValue []byte) error {
				if userField == 1 && userWire == 2 {
					userName = string(userValue)
				}
				return nil
			})
		case 4:
			state = number
		}
		_ = wire
		return nil
	})
	if err != nil || !strings.HasPrefix(name, "tenants/"+labTenant+"/gameSessionCreationTickets/") || config != "tenants/current/matchmakingConfigs/genesis" || !strings.HasPrefix(userName, "tenants/"+labTenant+"/users/u-") || state != 1 {
		t.Fatalf("Invalid ticket: name=%q config=%q user=%q state=%d error=%v", name, config, userName, state, err)
	}
	trackAlias := strings.Replace(name, "tenants/"+labTenant+"/", "tenants/current/", 1)
	trackRequest := protoBytes(nil, 1, []byte(trackAlias))
	tracked := callLabGRPC(t, server.Client(), server.URL+trackGameSessionCreationTicketPath, trackRequest, token)
	if tracked.status != "0" || len(tracked.body) < len(answer.body)+5 || !bytes.Equal(tracked.body[:len(answer.body)], answer.body) {
		t.Fatalf("Tracking did not return PENDING followed by SUCCEEDED: status=%s body=%x", tracked.status, tracked.body)
	}
	second := tracked.body[len(answer.body):]
	if second[0] != 0 || int(binary.BigEndian.Uint32(second[1:5])) != len(second)-5 {
		t.Fatalf("Invalid second frame: %x", second)
	}
	var succeededState uint64
	var sessionName, sessionHost string
	var sessionPort uint64
	var matchToken string
	var matchUserSession string
	err = visitProto(second[5:], func(field, wire, number uint64, value []byte) error {
		switch field {
		case 4:
			succeededState = number
		case 5:
			return visitProto(value, func(matchField, matchWire, _ uint64, matchValue []byte) error {
				if matchField == 2 && matchWire == 2 {
					matchUserSession = string(matchValue)
				}
				if matchField == 3 && matchWire == 2 {
					matchToken = string(matchValue)
				}
				return nil
			})
		case 6:
			return visitProto(value, func(sessionField, sessionWire, sessionNumber uint64, sessionValue []byte) error {
				switch sessionField {
				case 1:
					sessionName = string(sessionValue)
				case 8:
					sessionHost = string(sessionValue)
				case 9:
					sessionPort = sessionNumber
				}
				_ = sessionWire
				return nil
			})
		}
		_ = wire
		return nil
	})
	if err != nil || succeededState != 2 || !strings.Contains(sessionName, "/gameSessions/") || sessionHost != labTenant+".lp1.t.npln.srv.nintendo.net" || sessionPort != 443 || strings.Count(matchToken, ".") != 2 {
		t.Fatalf("Incomplete SUCCEEDED: state=%d session=%q host=%q port=%d token_valid=%t error=%v", succeededState, sessionName, sessionHost, sessionPort, strings.Count(matchToken, ".") == 2, err)
	}
	text := output.String()
	issueRequest := protoBytes(nil, 1, []byte(matchUserSession))
	issueRequest = protoBytes(issueRequest, 2, []byte(matchToken))
	gamesync := callLabGRPC(t, server.Client(), server.URL+gamesyncIssueTokenPath, issueRequest, "")
	if gamesync.status != "0" {
		t.Fatalf("Gamesync rejected ticket token: %s", gamesync.status)
	}
	if !strings.Contains(text, "Session description: create_time=true participant_create_time=true") {
		t.Fatal("Missing creation dates in the actual lab session")
	}
	if !strings.Contains(text, "Session token: profile="+profile) {
		t.Fatal("Requested profile was not selected")
	}
	if !strings.Contains(text, "config=\"genesis\" players=1 session=false capacity=0 properties=false") {
		t.Fatalf("Missing diagnostic: %s", text)
	}
	for _, secret := range []string{"PRIVATE_EXTERNAL_TOKEN", "PRIVATE_DELEGATION_TOKEN", token} {
		if strings.Contains(text, secret) {
			t.Fatal("Diagnostic printed a token")
		}
	}
}
