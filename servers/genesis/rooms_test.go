package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTwoPlayersShareRoomAndThirdCannotJoin(t *testing.T) {
	server := httptest.NewServer(handler(log.New(io.Discard, "", 0)))
	defer server.Close()
	post := func(path, player string) *http.Response {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"player": player})
		response, err := http.Post(server.URL+path, "application/json", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := post("/lab/rooms", "Ryujinx")
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("Create room: HTTP %d", response.StatusCode)
	}
	var room Room
	if err := json.NewDecoder(response.Body).Decode(&room); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()

	response = post("/lab/rooms/"+room.ID+"/join", "Switch")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("Join: HTTP %d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&room); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(room.Players) != 2 || room.Players[0] != "Ryujinx" || room.Players[1] != "Switch" {
		t.Fatalf("Unexpected players: %v", room.Players)
	}

	response = post("/lab/rooms/"+room.ID+"/join", "Third")
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("A full room must reject a third player: HTTP %d", response.StatusCode)
	}
}
