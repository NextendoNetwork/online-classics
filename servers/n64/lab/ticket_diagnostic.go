package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const createGameSessionCreationTicketPath = "/nn.npln.matchmaking.v1.GameSessionService/CreateGameSessionCreationTicket"
const trackGameSessionCreationTicketPath = "/nn.npln.matchmaking.v1.GameSessionService/TrackGameSessionCreationTicket"

func canonicalTicketName(name string) (string, string) {
	concretePrefix := "tenants/" + labTenant + "/gameSessionCreationTickets/"
	aliasPrefix := "tenants/current/gameSessionCreationTickets/"
	form := "unknown"
	switch {
	case strings.HasPrefix(name, concretePrefix):
		name = strings.TrimPrefix(name, concretePrefix)
		form = "concrete"
	case strings.HasPrefix(name, aliasPrefix):
		name = strings.TrimPrefix(name, aliasPrefix)
		form = "current"
	default:
		return "", form
	}
	if len(name) != 36 || name[8] != '-' || name[13] != '-' || name[18] != '-' || name[23] != '-' {
		return "", form
	}
	if _, err := hex.DecodeString(strings.ReplaceAll(name, "-", "")); err != nil {
		return "", form
	}
	return concretePrefix + name, form
}

func randomUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

type creationTicketSummary struct {
	parentOK       bool
	config         string
	users          int
	requestedMax   uint64
	hasProperties  bool
	hasGameSession bool
}

type pendingTicket struct {
	createdAt   time.Time
	owner       string
	config      string
	user        []byte
	request     requestedGameSession
	sessionName string
	userName    string
}

type requestedGameSession struct {
	capacity   uint64
	isPublic   bool
	password   string
	properties []byte
}

type ticketStore struct {
	genesisUserState    bool // Opt-in experiment: partial Genesis __stu seed.
	genesisStage        bool // Explicit experiment: __stg/All room document.
	genesisQueryMembers bool // Explicit experiment: participants in Genesis Query BASIC.
	queryKey            [32]byte
	sessions            map[string]*matchSession
	mu                  sync.Mutex
	tickets             map[string]pendingTicket
	issuedMatches       map[[32]byte]issuedMatch
	issuedGamesync      map[[32]byte]issuedMatch
	// In-memory Gamesync store per room (the ticket's GameSession).
	rooms map[string]*gamesyncRoom
}

func newTicketStore() *ticketStore {
	s := &ticketStore{sessions: make(map[string]*matchSession), tickets: make(map[string]pendingTicket), issuedMatches: make(map[[32]byte]issuedMatch), issuedGamesync: make(map[[32]byte]issuedMatch), rooms: make(map[string]*gamesyncRoom)}
	if _, err := rand.Read(s.queryKey[:]); err != nil {
		panic(err)
	}
	return s
}

// The host sends one UserDefinition. Preserve the requested configuration,
// but resolve the "current" alias to the host's specific local user.
func concreteTicketUser(payload []byte, uid string) ([]byte, error) {
	var input []byte
	err := visitProto(payload, func(field, wire, _ uint64, value []byte) error {
		if field != 2 {
			return nil
		}
		if wire != 2 {
			return errors.New("Ticket is not a message")
		}
		return visitProto(value, func(innerField, innerWire, _ uint64, innerValue []byte) error {
			if innerField == 3 {
				if innerWire != 2 || input != nil {
					return errors.New("Invalid user definition")
				}
				input = innerValue
			}
			return nil
		})
	})
	if err != nil || input == nil {
		return nil, errors.New("Missing user")
	}
	concrete := protoBytes(nil, 1, []byte("tenants/"+labTenant+"/users/"+uid))
	var name string
	err = visitProto(input, func(field, wire, _ uint64, value []byte) error {
		switch field {
		case 1:
			if wire != 2 {
				return errors.New("Invalid user name")
			}
			name = string(value)
		case 2, 3, 4:
			if wire != 2 {
				return errors.New("Invalid user attribute")
			}
			concrete = protoBytes(concrete, field, value)
		}
		return nil
	})
	if err != nil || !validUserPath(name, uid) {
		return nil, errors.New("Unauthorized user")
	}
	return concrete, nil
}

func requestedSession(payload []byte) (requestedGameSession, error) {
	var result requestedGameSession
	err := visitProto(payload, func(field, wire, _ uint64, value []byte) error {
		if field != 2 {
			return nil
		}
		if wire != 2 {
			return errors.New("Ticket is not a message")
		}
		return visitProto(value, func(innerField, innerWire, _ uint64, innerValue []byte) error {
			if innerField != 6 {
				return nil
			}
			if innerWire != 2 {
				return errors.New("Game session is not a message")
			}
			return visitProto(innerValue, func(gsField, gsWire, gsNumber uint64, gsValue []byte) error {
				switch gsField {
				case 2:
					if gsWire != 0 {
						return errors.New("Invalid capacity")
					}
					result.capacity = gsNumber
				case 5:
					if gsWire != 0 {
						return errors.New("Invalid visibility")
					}
					result.isPublic = gsNumber != 0
				case 6:
					if gsWire != 2 {
						return errors.New("Invalid password")
					}
					result.password = string(gsValue)
				case 11:
					if gsWire != 2 {
						return errors.New("Invalid properties")
					}
					result.properties = append([]byte(nil), gsValue...)
				}
				return nil
			})
		})
	})
	return result, err
}

func ticketPendingResponse(name string, ticket pendingTicket) []byte {
	response := protoBytes(nil, 1, []byte(name))
	response = protoBytes(response, 2, []byte(ticket.config))
	response = protoBytes(response, 3, ticket.user)
	return protoVarint(response, 4, 1)
}

func ticketSucceededResponse(name string, ticket pendingTicket, token string) []byte {
	userPath := "tenants/" + labTenant + "/users/" + ticket.owner
	userSession := protoBytes(nil, 1, []byte(ticket.userName))
	userSession = protoBytes(userSession, 2, []byte(userPath))
	userSession = protoVarint(userSession, 5, 2) // ACTIVE
	// Creation timestamp shared by the session and its first participant.
	// Preserved since Create; Track does not change the same object's timestamp.
	var created []byte
	if !ticket.createdAt.IsZero() {
		created = protoVarint(nil, 1, uint64(ticket.createdAt.Unix()))
		created = protoVarint(created, 2, uint64(ticket.createdAt.Nanosecond()))
		userSession = protoBytes(userSession, 4, created)
	}

	capacity := sessionCapacity(ticket)
	session := protoBytes(nil, 1, []byte(ticket.sessionName))
	session = protoVarint(session, 2, capacity)
	session = protoVarint(session, 3, 1)
	session = protoVarint(session, 4, 1)
	if ticket.request.isPublic {
		session = protoVarint(session, 5, 1)
	}
	if ticket.request.password != "" {
		session = protoBytes(session, 6, []byte(ticket.request.password))
	}
	session = protoVarint(session, 7, 2) // ACTIVE
	// The same NPLN address already redirected to the lab in Ryujinx.
	// This only exposes the next step; it does not implement transport.
	session = protoBytes(session, 8, []byte(labTenant+".lp1.t.npln.srv.nintendo.net"))
	session = protoVarint(session, 9, 443)
	if created != nil {
		session = protoBytes(session, 10, created)
	}
	if ticket.request.properties != nil {
		session = protoBytes(session, 11, ticket.request.properties)
	}
	session = protoBytes(session, 12, userSession)

	matched := protoBytes(nil, 1, ticket.user)
	matched = protoBytes(matched, 2, []byte(ticket.userName))
	matched = protoBytes(matched, 3, []byte(token))
	response := protoBytes(nil, 1, []byte(name))
	response = protoBytes(response, 2, []byte(ticket.config))
	response = protoBytes(response, 3, ticket.user)
	response = protoVarint(response, 4, 2) // SUCCEEDED
	response = protoBytes(response, 5, matched)
	return protoBytes(response, 6, session)
}

// Extracts only nonsensitive metadata. Tokens and player attributes
// are never retained or printed.
func summarizeCreationTicket(payload []byte) (creationTicketSummary, error) {
	var summary creationTicketSummary
	err := visitProto(payload, func(field, wire, number uint64, value []byte) error {
		switch field {
		case 1:
			if wire != 2 {
				return errors.New("Parent is not a string")
			}
			parent := string(value)
			summary.parentOK = parent == "tenants/current" || parent == "tenants/"+labTenant
		case 2:
			if wire != 2 {
				return errors.New("Ticket is not a message")
			}
			return visitProto(value, func(innerField, innerWire, innerNumber uint64, innerValue []byte) error {
				switch innerField {
				case 2:
					if innerWire != 2 {
						return errors.New("Configuration is not a string")
					}
					config := string(innerValue)
					if len(config) > 160 {
						return errors.New("Configuration too long")
					}
					summary.config = config
				case 3:
					if innerWire != 2 {
						return errors.New("User definition is not a message")
					}
					summary.users++
				case 6:
					if innerWire != 2 {
						return errors.New("Game session is not a message")
					}
					summary.hasGameSession = true
					return visitProto(innerValue, func(gsField, gsWire, gsNumber uint64, _ []byte) error {
						switch gsField {
						case 2:
							if gsWire != 0 {
								return errors.New("Max participants is not a number")
							}
							summary.requestedMax = gsNumber
						case 11:
							if gsWire != 2 {
								return errors.New("Properties is not a message")
							}
							summary.hasProperties = true
						}
						return nil
					})
				}
				_ = innerNumber
				return nil
			})
		}
		_ = number
		return nil
	})
	return summary, err
}

func (s *ticketStore) createGameSessionCreationTicket(a *labAuth, logger *log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validGRPC(w, r) {
			return
		}
		uid, ok := a.authorizedUser(w, r)
		if !ok {
			return
		}
		payload, err := grpcRequestPayload(r)
		if err != nil {
			grpcStatus(w, "3", "Invalid frame", nil)
			return
		}
		summary, err := summarizeCreationTicket(payload)
		if err != nil || !summary.parentOK || summary.users != 1 || summary.config == "" {
			grpcStatus(w, "3", "Invalid ticket", nil)
			return
		}
		user, err := concreteTicketUser(payload, uid)
		if err != nil {
			grpcStatus(w, "3", "Invalid ticket user", nil)
			return
		}
		requested, err := requestedSession(payload)
		if err != nil {
			grpcStatus(w, "3", "Invalid game session", nil)
			return
		}
		// Only the last configuration-path component. No tokens, user names,
		// attributes or binary bodies in logs.
		config := summary.config
		if slash := strings.LastIndexByte(config, '/'); slash >= 0 {
			config = config[slash+1:]
		}
		logger.Printf("CreateGameSessionCreationTicket diagnostic: config=%q players=%d session=%t capacity=%d properties=%t",
			config, summary.users, summary.hasGameSession, summary.requestedMax, summary.hasProperties)
		id, err := randomUUID()
		if err != nil {
			grpcStatus(w, "13", "Could not create the ticket", nil)
			return
		}
		name := "tenants/" + labTenant + "/gameSessionCreationTickets/" + id
		sessionID, err := randomUUID()
		if err != nil {
			grpcStatus(w, "13", "Could not create the session", nil)
			return
		}
		participantID, err := randomUUID()
		if err != nil {
			grpcStatus(w, "13", "Could not create the participant", nil)
			return
		}
		sessionName := "tenants/" + labTenant + "/gameSessions/" + sessionID
		ticket := pendingTicket{
			createdAt: time.Now().UTC(),
			owner:     uid, config: summary.config, user: user, request: requested,
			sessionName: sessionName,
			userName:    sessionName + "/userSessions/" + participantID,
		}
		s.mu.Lock()
		s.tickets[name] = ticket
		s.mu.Unlock()
		logger.Printf("Creation ticket pending: id=%s", id)
		grpcStatus(w, "0", "", ticketPendingResponse(name, ticket))
	}
}

func (s *ticketStore) trackGameSessionCreationTicket(a *labAuth, logger *log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validGRPC(w, r) {
			return
		}
		uid, ok := a.authorizedUser(w, r)
		if !ok {
			return
		}
		payload, err := grpcRequestPayload(r)
		if err != nil {
			grpcStatus(w, "3", "Invalid frame", nil)
			return
		}
		name, err := protoStringField1(payload)
		if err != nil {
			grpcStatus(w, "3", "Invalid ticket", nil)
			return
		}
		name, form := canonicalTicketName(name)
		if name == "" {
			logger.Printf("Ticket tracking: reference=%s", form)
			grpcStatus(w, "3", "Invalid ticket reference", nil)
			return
		}
		s.mu.Lock()
		ticket, exists := s.tickets[name]
		s.mu.Unlock()
		logger.Printf("Ticket tracking: reference=%s found=%t", form, exists)
		if !exists {
			grpcStatus(w, "5", "Ticket does not exist", nil)
			return
		}
		if ticket.owner != uid {
			grpcStatus(w, "7", "Ticket belongs to another user", nil)
			return
		}
		sessionToken, err := a.sessionToken(ticket, time.Now())
		if err != nil {
			grpcStatus(w, "13", "Could not issue local token", nil)
			return
		}
		logger.Printf("Session token: profile=%s", a.sessionProfile)
		logger.Printf("Session description: create_time=%t participant_create_time=%t", !ticket.createdAt.IsZero(), !ticket.createdAt.IsZero())
		w.WriteHeader(http.StatusOK)
		if writeGRPCFrame(w, ticketPendingResponse(name, ticket)) != nil {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
		s.rememberMatchToken(ticket, sessionToken, time.Now())
		if err := s.publishMatchSession(ticket); err != nil {
			w.Header().Set("Grpc-Status", err.code)
			return
		}
		if writeGRPCFrame(w, ticketSucceededResponse(name, ticket, sessionToken)) != nil {
			return
		}
		w.Header().Set("Grpc-Status", "0")
		logger.Printf("Creation ticket completed in lab: session=%q", ticket.sessionName)
	}
}
