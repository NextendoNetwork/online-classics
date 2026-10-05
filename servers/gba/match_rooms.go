package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"
)

const joinGameSessionPath = "/nn.npln.matchmaking.v1.GameSessionService/JoinGameSession"
const getGameSessionPath = "/nn.npln.matchmaking.v1.GameSessionService/GetGameSession"

type matchSession struct {
	host    pendingTicket
	members map[string]pendingTicket // UID -> own session; one slot per user.
	closed  bool
}

func sessionCapacity(ticket pendingTicket) uint64 {
	n := ticket.request.capacity
	if n < 2 || n > 16 {
		return 2
	}
	return n
}

func canonicalMatchSession(name string) (string, bool) {
	name = strings.Replace(name, "tenants/current/", "tenants/"+labTenant+"/", 1)
	prefix := "tenants/" + labTenant + "/gameSessions/"
	if !strings.HasPrefix(name, prefix) {
		return "", false
	}
	id := strings.TrimPrefix(name, prefix)
	if id == "" || len(id) > gsMaxSegment || !gsSafeSegment(id) || strings.Contains(id, "/") {
		return "", false
	}
	return name, true
}

func (s *ticketStore) publishMatchSession(host pendingTicket) *gsError {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.sessions[host.sessionName]; existing != nil {
		if existing.closed {
			return gsFail("9", "Room closed")
		}
		return nil
	}
	s.sessions[host.sessionName] = &matchSession{host: host, members: map[string]pendingTicket{host.owner: host}}
	return nil
}

// Called outside the Gamesync lock. Closing the host's last channel ends
// this room; a guest's departure releases their slot. This first version
// has no reconnect grace period: the client must join again.
func (s *ticketStore) leaveMatchSession(sessionName, usid string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.sessions[sessionName]
	if session == nil {
		return
	}
	if resourceLeaf(session.host.userName) == usid {
		session.closed = true
	}
	for uid, member := range session.members {
		if resourceLeaf(member.userName) == usid {
			delete(session.members, uid)
		}
	}
	for key, issued := range s.issuedMatches {
		if issued.ticket.sessionName == sessionName && (session.closed || resourceLeaf(issued.ticket.userName) == usid) {
			delete(s.issuedMatches, key)
		}
	}
	for key, issued := range s.issuedGamesync {
		if issued.ticket.sessionName == sessionName && (session.closed || resourceLeaf(issued.ticket.userName) == usid) {
			delete(s.issuedGamesync, key)
		}
	}
}

// A join that never opens Gamesync cannot reserve J2 indefinitely.
// Caller holds s.mu; reading open follows the store -> room lock order.
func (s *ticketStore) expireJoinReservations(session *matchSession, now time.Time) {
	room := s.rooms[session.host.sessionName]
	for uid, member := range session.members {
		if uid == session.host.owner || now.Sub(member.createdAt) < 5*time.Minute {
			continue
		}
		active := false
		if room != nil {
			room.mu.Lock()
			active = room.open[resourceLeaf(member.userName)] > 0
			room.mu.Unlock()
		}
		if active {
			continue
		}
		delete(session.members, uid)
		for key, issued := range s.issuedMatches {
			if issued.ticket.userName == member.userName {
				delete(s.issuedMatches, key)
			}
		}
		for key, issued := range s.issuedGamesync {
			if issued.ticket.userName == member.userName {
				delete(s.issuedGamesync, key)
			}
		}
	}
}

func encodeMatchUserSession(ticket pendingTicket) []byte {
	b := protoBytes(nil, 1, []byte(ticket.userName))
	b = protoBytes(b, 2, []byte("tenants/"+labTenant+"/users/"+ticket.owner))
	_ = visitProto(ticket.user, func(f, w, _ uint64, v []byte) error {
		if w == 2 {
			if f == 2 {
				b = protoBytes(b, 7, v)
			}
			if f == 3 {
				b = protoBytes(b, 3, v)
			}
			if f == 4 {
				b = protoBytes(b, 6, v)
			}
		}
		return nil
	})
	b = protoBytes(b, 4, protoTimestamp(ticket.createdAt))
	return protoVarint(b, 5, 2)
}

func encodeMatchSession(session *matchSession, view uint64) []byte {
	host := session.host
	b := protoBytes(nil, 1, []byte(host.sessionName))
	capacity := sessionCapacity(host)
	count := uint64(len(session.members))
	b = protoVarint(b, 2, capacity)
	b = protoVarint(b, 3, count)
	if count < capacity {
		b = protoVarint(b, 4, 1)
	}
	if host.request.isPublic {
		b = protoVarint(b, 5, 1)
	}
	// Do not disclose the password to a room-search caller.
	b = protoVarint(b, 7, 2)
	b = protoBytes(b, 8, []byte(labTenant+".lp1.t.npln.srv.nintendo.net"))
	b = protoVarint(b, 9, 443)
	b = protoBytes(b, 10, protoTimestamp(host.createdAt))
	if host.request.properties != nil {
		b = protoBytes(b, 11, host.request.properties)
	}
	if view != 1 {
		var names []string
		for uid := range session.members {
			names = append(names, uid)
		}
		sort.Strings(names)
		for _, uid := range names {
			b = protoBytes(b, 12, encodeMatchUserSession(session.members[uid]))
		}
	}
	return b
}

func (s *ticketStore) matchQueryView(q matchQuery, session *matchSession) uint64 {
	if s.genesisQueryMembers && q.view == 1 && resourceLeaf(q.config) == "LCLA6" && (resourceLeaf(session.host.config) == "LCLA6-2P" || resourceLeaf(session.host.config) == "LCLA6-4P") {
		return 2
	}
	return q.view
}

// Prints no IDs, properties or attributes. Only compares the host ID with
// the NSA already configured for the same UID in the authorized pair.
func logMatchResponseStructure(logger *log.Logger, pair *localFriendPair, session *matchSession, requestedView, responseView uint64) {
	_, props, err := gsParseDocument(protoBytes(protoBytes(nil, 1, []byte("filter")), 2, session.host.request.properties))
	raw, present := gsLookup(props, "hostNsaId")
	var hostID []byte
	if err == nil && present {
		_ = visitProto(raw, func(f, w, _ uint64, v []byte) error {
			if f == 8 && w == 2 {
				hostID = v
			}
			return nil
		})
	}
	var expected []byte
	if pair != nil {
		pair.mu.Lock()
		for _, peer := range pair.peers {
			if peer.uid == session.host.owner {
				expected, _ = hex.DecodeString(peer.nsa)
				break
			}
		}
		pair.mu.Unlock()
	}
	big, little := false, false
	if len(expected) == 8 && len(hostID) == 8 {
		big = bytes.Equal(expected, hostID)
		reversed := append([]byte(nil), expected...)
		for i := 0; i < 4; i++ {
			reversed[i], reversed[7-i] = reversed[7-i], reversed[i]
		}
		little = bytes.Equal(reversed, hostID)
	}
	logger.Printf("QueryGameSessions reply: requested_view=%d encoded_view=%d participants_included=%t participants=%d capacity=%d public=%t host_nsa_present=%t host_nsa_bytes=%d nsa_configured=%t host_nsa_matches_big=%t host_nsa_matches_little=%t", requestedView, responseView, responseView != 1, len(session.members), sessionCapacity(session.host), session.host.request.isPublic, present, len(hostID), len(expected) == 8, big, little)
}

type matchQuery struct {
	tenant, config              string
	cursor                      string
	pageSize                    int
	binding                     [32]byte
	view, vacancy, participants uint64
	properties                  []gsField
	users                       []string
}

func parseMatchQuery(data []byte) (matchQuery, *gsError) {
	q := matchQuery{pageSize: 100}
	var binding []byte
	err := visitProto(data, func(f, w, n uint64, v []byte) error {
		if f != 9 {
			if w == 2 {
				binding = protoBytes(binding, f, v)
			}
			if w == 0 {
				binding = protoVarint(binding, f, n)
			}
		}
		switch f {
		case 1, 3, 7, 9:
			if w != 2 {
				return errors.New("Invalid string")
			}
			switch f {
			case 1:
				q.tenant = string(v)
			case 3:
				q.config = string(v)
			case 7:
				q.users = append(q.users, string(v))
			case 9:
				q.cursor = string(v)
			}
		case 2, 4, 5, 8:
			if w != 0 || n > 1<<31-1 {
				return errors.New("Invalid integer")
			}
			switch f {
			case 2:
				q.view = n
			case 4:
				q.vacancy = n
			case 5:
				q.participants = n
			case 8:
				if n > 0 {
					q.pageSize = int(n)
					if q.pageSize > 100 {
						q.pageSize = 100
					}
				}
			}
		case 6:
			if w != 2 {
				return errors.New("Invalid properties")
			}
			_, fields, perr := gsParseDocument(protoBytes(protoBytes(nil, 1, []byte("filter")), 2, v))
			if perr != nil {
				return errors.New("Invalid properties")
			}
			q.properties = fields
		}
		return nil
	})
	if err != nil || len(q.cursor) > 2048 {
		return q, gsFail("3", "Invalid query")
	}
	q.binding = sha256.Sum256(binding)
	if q.tenant != "tenants/current" && q.tenant != "tenants/"+labTenant {
		return q, gsFail("3", "Incorrect tenant")
	}
	if q.view > 3 || len(q.users) > 32 {
		return q, gsFail("3", "Invalid query")
	}
	for _, user := range q.users {
		if !strings.HasPrefix(user, "tenants/"+labTenant+"/users/") && !strings.HasPrefix(user, "tenants/current/users/") {
			return q, gsFail("3", "User belongs to another tenant")
		}
	}
	return q, nil
}

func (q matchQuery) matches(session *matchSession, caller string) bool {
	return q.exclusionReason(session, caller) == ""
}

// Search and creation names are distinct resources. Mapping observed
// in Genesis 3.1.1 and GBA 3.3.0 local traces; unrelated names stay excluded.
func matchSearchConfig(search, creation string) bool {
	search, creation = resourceLeaf(search), resourceLeaf(creation)
	return search == "" || search == creation || (search == "LCLA6" && (creation == "LCLA6-2P" || creation == "LCLA6-4P"))
}

// Genesis and GBA use search parameter names that differ from room-field names.
// GBA mapping observed in the October 4, 2026 local room discovery trace.
// Only the observed LCLA6 creation pairs accept these aliases; values must match exactly.
func (q matchQuery) propertyValue(session *matchSession, props []gsField, key string) ([]byte, bool) {
	exact, exists := gsLookup(props, key)
	// The exact key takes precedence; an alias cannot rescue an incompatible value.
	if exists {
		return exact, true
	}
	if resourceLeaf(q.config) != "LCLA6" || (resourceLeaf(session.host.config) != "LCLA6-2P" && resourceLeaf(session.host.config) != "LCLA6-4P") {
		return exact, exists
	}
	var alias string
	switch key {
	case "ConsoleName":
		alias = "consoleName"
	case "ApplicationVersion":
		alias = "applicationVersion"
	default:
		return exact, exists
	}
	aliased, aliasExists := gsLookup(props, alias)
	return aliased, aliasExists
}

func (q matchQuery) exclusionReason(session *matchSession, caller string) string {
	if session.closed {
		return "closed"
	}
	count := uint64(len(session.members))
	capacity := sessionCapacity(session.host)
	if count > capacity || capacity-count < q.vacancy || count < q.participants {
		return "capacity"
	}
	if !matchSearchConfig(q.config, session.host.config) {
		return "config"
	}
	if len(q.users) > 0 {
		found := false
		for _, user := range q.users {
			uid := resourceLeaf(user)
			if uid == "current" {
				uid = caller
			}
			if _, ok := session.members[uid]; ok {
				found = true
			}
		}
		if !found {
			return "users"
		}
	}
	_, props, err := gsParseDocument(protoBytes(protoBytes(nil, 1, []byte("filter")), 2, session.host.request.properties))
	if err != nil {
		return "properties"
	}
	for _, expected := range q.properties {
		actual, ok := q.propertyValue(session, props, expected.key)
		if !ok || !bytes.Equal(actual, expected.value) {
			return "properties"
		}
	}
	return ""
}

// Identifies the comparison that excludes a room without logging keys or values.
// The hash correlates the same property across subsequent attempts.
func matchPropertySchemaLabel(key string) string {
	// Known version/platform labels only; do not publish arbitrary keys.
	switch key {
	case "ApplicationVersion", "ConsoleName", "applicationVersion", "consoleName",
		"application_version", "console_name", "AppVersion", "GameVersion", "Version",
		"Console", "Platform", "PlatformName", "version", "console", "platform", "cn", "av":
		return key
	default:
		return "{key}"
	}
}

func logMatchPropertyComparison(logger *log.Logger, q matchQuery, session *matchSession) {
	_, props, err := gsParseDocument(protoBytes(protoBytes(nil, 1, []byte("filter")), 2, session.host.request.properties))
	logger.Printf("QueryGameSessions properties: room_parse_valid=%t room_properties=%d query_properties=%d", err == nil, len(props), len(q.properties))
	if err != nil {
		return
	}
	for i, prop := range props {
		keyHash := sha256.Sum256([]byte(prop.key))
		kind, _ := gsValueKind(prop.value)
		logger.Printf("QueryGameSessions room_property: index=%d key_schema=%q key_hash=%x key_length=%d type=%d bytes=%d", i, matchPropertySchemaLabel(prop.key), keyHash[:8], len(prop.key), kind, len(prop.value))
	}
	for i, expected := range q.properties {
		actual, exists := gsLookup(props, expected.key)
		expectedKind, _ := gsValueKind(expected.value)
		var actualKind uint64
		if exists {
			actualKind, _ = gsValueKind(actual)
		}
		keyHash := sha256.Sum256([]byte(expected.key))
		logger.Printf("QueryGameSessions property: index=%d key_schema=%q key_hash=%x key_length=%d exists_in_room=%t query_type=%d room_type=%d query_bytes=%d room_bytes=%d encoded_value_equal=%t", i, matchPropertySchemaLabel(expected.key), keyHash[:8], len(expected.key), exists, expectedKind, actualKind, len(expected.value), len(actual), exists && bytes.Equal(actual, expected.value))
		for j, prop := range props {
			hostHash := sha256.Sum256([]byte(prop.key))
			logger.Printf("QueryGameSessions mapping: query_index=%d room_index=%d room_key_hash=%x same_key=%t same_key_case_insensitive=%t same_encoded_value=%t", i, j, hostHash[:8], expected.key == prop.key, strings.EqualFold(expected.key, prop.key), bytes.Equal(expected.value, prop.value))
		}
	}
}

func (s *ticketStore) queryMatchSessions(a *labAuth, logger *log.Logger, lab *roomStore) http.HandlerFunc {
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
		// Legacy probe compatibility: an empty request uses /lab only.
		if len(payload) == 0 {
			copyRequest := r.Clone(r.Context())
			copyRequest.Body = io.NopCloser(bytes.NewReader([]byte{0, 0, 0, 0, 0}))
			lab.queryGameSessions(w, copyRequest)
			return
		}
		q, perr := parseMatchQuery(payload)
		if perr != nil {
			grpcStatus(w, perr.code, perr.message, nil)
			return
		}
		a.identityDiagnostic.record(r, "query", uid, q.users)
		var last string
		if q.cursor != "" {
			raw, err := base64.RawURLEncoding.DecodeString(q.cursor)
			if err != nil || len(raw) < 64 {
				grpcStatus(w, "3", "Invalid cursor", nil)
				return
			}
			mac := hmac.New(sha256.New, s.queryKey[:])
			mac.Write([]byte(uid))
			mac.Write(raw[32:])
			if !hmac.Equal(raw[:32], mac.Sum(nil)) || !bytes.Equal(raw[32:64], q.binding[:]) {
				grpcStatus(w, "3", "Unauthorized or invalid cursor", nil)
				return
			}
			last = string(raw[64:])
		}
		s.mu.Lock()
		var names []string
		matchedAliases := 0
		excluded := make(map[string]int)
		knownUsers, currentUsers, selfUsers := 0, 0, 0
		for _, user := range q.users {
			id := resourceLeaf(user)
			if id == "current" {
				currentUsers++
				id = uid
			}
			if id == uid {
				selfUsers++
			}
			for _, session := range s.sessions {
				if _, ok := session.members[id]; ok {
					knownUsers++
					break
				}
			}
		}
		for name, session := range s.sessions {
			s.expireJoinReservations(session, time.Now())
			if name <= last {
				excluded["cursor"]++
				continue
			}
			if reason := q.exclusionReason(session, uid); reason != "" {
				excluded[reason]++
				if reason == "properties" {
					logMatchPropertyComparison(logger, q, session)
				}
				continue
			}
			room := s.rooms[name]
			if room == nil {
				excluded["inactive"]++
				continue
			}
			room.mu.Lock()
			active := room.open[resourceLeaf(session.host.userName)] > 0
			room.mu.Unlock()
			if active {
				names = append(names, name)
				_, props, _ := gsParseDocument(protoBytes(protoBytes(nil, 1, []byte("filter")), 2, session.host.request.properties))
				for _, expected := range q.properties {
					if _, exact := gsLookup(props, expected.key); !exact {
						if _, aliased := q.propertyValue(session, props, expected.key); aliased {
							matchedAliases++
						}
					}
				}
			} else {
				excluded["inactive"]++
			}
		}
		sort.Strings(names)
		var response []byte
		count := len(names)
		if count > q.pageSize {
			count = q.pageSize
		}
		for _, name := range names[:count] {
			session := s.sessions[name]
			responseView := s.matchQueryView(q, session)
			logMatchResponseStructure(logger, a.friendPair, session, q.view, responseView)
			response = protoBytes(response, 1, encodeMatchSession(session, responseView))
		}
		if count < len(names) {
			data := append(append([]byte(nil), q.binding[:]...), []byte(names[count-1])...)
			mac := hmac.New(sha256.New, s.queryKey[:])
			mac.Write([]byte(uid))
			mac.Write(data)
			response = protoBytes(response, 2, []byte(base64.RawURLEncoding.EncodeToString(append(mac.Sum(nil), data...))))
		}
		total := len(s.sessions)
		s.mu.Unlock()
		logger.Printf("Actual QueryGameSessions: rooms=%d user_filter=%d properties=%d config=%q view=%d aliased_properties=%d", count, len(q.users), len(q.properties), resourceLeaf(q.config), q.view, matchedAliases)
		logger.Printf("QueryGameSessions filters: registered=%d excluded_config=%d excluded_users=%d excluded_properties=%d excluded_capacity=%d closed=%d inactive=%d cursor=%d current_users=%d own_users=%d known_users=%d", total, excluded["config"], excluded["users"], excluded["properties"], excluded["capacity"], excluded["closed"], excluded["inactive"], excluded["cursor"], currentUsers, selfUsers, knownUsers)
		grpcStatus(w, "0", "", response)
	}
}

func (s *ticketStore) joinMatchSession(a *labAuth, logger *log.Logger) http.HandlerFunc {
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
		var name, password string
		var definition []byte
		var delegationCount, emptyDelegations, requestedTokens, foreignTokens int
		err = visitProto(payload, func(f, w, _ uint64, v []byte) error {
			if f >= 1 && f <= 5 && w != 2 {
				return errors.New("Invalid field")
			}
			switch f {
			case 1:
				name = string(v)
			case 2:
				password = string(v)
			case 3:
				if definition != nil {
					return errors.New("Only one participant")
				}
				definition = v
			case 4:
				if len(v) == 0 {
					emptyDelegations++
				} else {
					delegationCount++
				}
			case 5:
				requestedTokens++
				if !validUserPath(string(v), uid) && string(v) != "tenants/current/users/"+uid {
					foreignTokens++
				}
			}
			return nil
		})
		canonical, valid := canonicalMatchSession(name)
		if err != nil || !valid || definition == nil || len(password) > 128 {
			grpcStatus(w, "3", "Invalid join request", nil)
			return
		}
		// A self token is already returned in MatchedUserSession field 3.
		// Empty repeated strings carry no delegation credential. Never mint
		// credentials for another user or log supplied credentials.
		logger.Printf("JoinGameSession request: delegations=%d empty_delegations=%d requested_tokens=%d own_tokens=%d foreign_or_invalid_tokens=%d", delegationCount, emptyDelegations, requestedTokens, requestedTokens-foreignTokens, foreignTokens)
		if delegationCount > 0 {
			grpcStatus(w, "12", "Delegation not implemented", nil)
			return
		}
		if foreignTokens > 0 {
			grpcStatus(w, "7", "Unauthorized or invalid user token", nil)
			return
		}
		user, err := concreteTicketUser(protoBytes(nil, 2, protoBytes(nil, 3, definition)), uid)
		if err != nil {
			grpcStatus(w, "7", "Unauthorized joining user", nil)
			return
		}
		id, err := randomUUID()
		if err != nil {
			grpcStatus(w, "13", "Could not create participant", nil)
			return
		}
		s.mu.Lock()
		session := s.sessions[canonical]
		if session == nil || session.closed {
			s.mu.Unlock()
			grpcStatus(w, "5", "Room missing or closed", nil)
			return
		}
		if password != session.host.request.password {
			s.mu.Unlock()
			grpcStatus(w, "7", "Incorrect password", nil)
			return
		}
		s.expireJoinReservations(session, time.Now())
		member, exists := session.members[uid]
		if !exists && uint64(len(session.members)) >= sessionCapacity(session.host) {
			s.mu.Unlock()
			grpcStatus(w, "8", "Room full", nil)
			return
		}
		if !exists {
			member = pendingTicket{createdAt: time.Now().UTC(), owner: uid, user: user, config: session.host.config, request: session.host.request, sessionName: canonical, userName: canonical + "/userSessions/" + id}
		}
		token, err := a.sessionToken(member, time.Now())
		if err != nil {
			s.mu.Unlock()
			grpcStatus(w, "13", "Could not issue local token", nil)
			return
		}
		session.members[uid] = member
		s.issuedMatches[sha256.Sum256([]byte(token))] = issuedMatch{ticket: member, expires: time.Now().Add(time.Hour)}
		matched := protoBytes(nil, 1, member.user)
		matched = protoBytes(matched, 2, []byte(member.userName))
		matched = protoBytes(matched, 3, []byte(token))
		response := protoBytes(nil, 1, matched)
		response = protoBytes(response, 2, encodeMatchSession(session, 2))
		count := len(session.members)
		s.mu.Unlock()
		logger.Printf("Actual JoinGameSession: participants=%d retry=%t", count, exists)
		grpcStatus(w, "0", "", response)
	}
}

func (s *ticketStore) getMatchSession(a *labAuth, logger *log.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !validGRPC(w, r) {
			return
		}
		if _, ok := a.authorizedUser(w, r); !ok {
			return
		}
		payload, err := grpcRequestPayload(r)
		var name string
		var view uint64
		if err == nil {
			err = visitProto(payload, func(f, w, n uint64, v []byte) error {
				if f == 1 {
					if w != 2 {
						return errors.New("Invalid name")
					}
					name = string(v)
				}
				if f == 2 {
					if w != 0 || n > 3 {
						return errors.New("Invalid view")
					}
					view = n
				}
				return nil
			})
		}
		name, ok := canonicalMatchSession(name)
		if err != nil || !ok {
			grpcStatus(w, "3", "Invalid session", nil)
			return
		}
		s.mu.Lock()
		session := s.sessions[name]
		if session == nil || session.closed {
			s.mu.Unlock()
			grpcStatus(w, "5", "Room missing or closed", nil)
			return
		}
		response := encodeMatchSession(session, view)
		s.mu.Unlock()
		logger.Print("Actual GetGameSession: room found")
		grpcStatus(w, "0", "", response)
	}
}
