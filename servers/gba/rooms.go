package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// Room represents a test room. It does not yet use the NPLN format.
type Room struct {
	ID      string   `json:"id"`
	Host    string   `json:"host"`
	Players []string `json:"players"`
}

type roomStore struct {
	mu    sync.Mutex
	rooms map[string]Room
}

func newRoomStore() *roomStore {
	return &roomStore{rooms: make(map[string]Room)}
}

func (s *roomStore) register(mux *http.ServeMux) {
	mux.HandleFunc("/lab/rooms", s.listOrCreate)
	mux.HandleFunc("/lab/rooms/", s.join)
	mux.HandleFunc(queryGameSessionsPath, s.queryGameSessions)
}

func (s *roomStore) snapshot() []Room {
	s.mu.Lock()
	defer s.mu.Unlock()
	rooms := make([]Room, 0, len(s.rooms))
	for _, room := range s.rooms {
		rooms = append(rooms, room)
	}
	return rooms
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func readPlayer(w http.ResponseWriter, r *http.Request) (string, bool) {
	var input struct {
		Player string `json:"player"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || strings.TrimSpace(input.Player) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Send JSON containing player"})
		return "", false
	}
	return strings.TrimSpace(input.Player), true
}

func (s *roomStore) listOrCreate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.snapshot())
	case http.MethodPost:
		player, ok := readPlayer(w, r)
		if !ok {
			return
		}
		idBytes := make([]byte, 4)
		if _, err := rand.Read(idBytes); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Could not create the identifier"})
			return
		}
		room := Room{ID: hex.EncodeToString(idBytes), Host: player, Players: []string{player}}
		s.mu.Lock()
		s.rooms[room.ID] = room
		s.mu.Unlock()
		writeJSON(w, http.StatusCreated, room)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *roomStore) join(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/join") {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/lab/rooms/"), "/join")
	id = strings.TrimSuffix(id, "/")
	player, ok := readPlayer(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	room, exists := s.rooms[id]
	if !exists {
		s.mu.Unlock()
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Room does not exist"})
		return
	}
	for _, current := range room.Players {
		if current == player {
			s.mu.Unlock()
			writeJSON(w, http.StatusOK, room)
			return
		}
	}
	if len(room.Players) >= 2 {
		s.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Room full"})
		return
	}
	room.Players = append(room.Players, player)
	s.rooms[id] = room
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, room)
}
