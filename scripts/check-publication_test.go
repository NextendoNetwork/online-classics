package main

import (
	"strings"
	"testing"
)

func TestPublicationRejectsPrivateRuntimeContent(t *testing.T) {
	for _, item := range []struct{ name, content, label string }{
		{"private-nso/config.json", "{}", "private runtime directory"},
		{"config.local.json", "{}", "local configuration"},
		{"game.nsp", "fixture", "file type outside the publication allowlist"},
		{"source.go", "-----BEGIN " + "PRIVATE KEY-----", "private signing material"},
		{"notes.md", "Bearer " + strings.Repeat("a", 64), "embedded bearer credential"},
		{"notes.md", "192.168." + "7.8", "personal LAN address; use synthetic fixtures"},
	} {
		found := false
		for _, label := range inspect(item.name, []byte(item.content)) {
			if label == item.label {
				found = true
			}
		}
		if !found {
			t.Fatalf("content category not rejected: %s", item.label)
		}
	}
	for _, name := range []string{"servers/gba/main.go", "deployment/config.example.json", "deployment/online-classics@.service", "LICENSE", "example.env"} {
		if len(inspect(name, []byte("source fixture"))) != 0 {
			t.Fatalf("legitimate file rejected: %s", name)
		}
	}
}
