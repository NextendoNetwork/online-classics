// Review Git's index, never the unreviewed working tree. Report file names and
// fixed labels only; matched credentials or private addresses are not printed.
package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

var allowedSuffix = map[string]bool{".go": true, ".mod": true, ".sum": true, ".md": true, ".json": true, ".yml": true, ".yaml": true, ".service": true}
var allowedName = map[string]bool{".gitignore": true, "LICENSE": true, "example.env": true}
var privateDirectory = map[string]bool{"artifacts": true, "work": true, "captures": true, "profiles": true, "private-nso": true, "private-gpu": true}
var prohibited = []struct {
	label   string
	pattern *regexp.Regexp
}{
	{"personal LAN address; use synthetic fixtures", regexp.MustCompile(`\b192\.168\.\d{1,3}\.\d{1,3}\b`)},
	{"private signing material", regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`)},
	{"embedded bearer credential", regexp.MustCompile(`Bearer\s+[A-Za-z0-9_./+=-]{40,}`)},
	{"embedded GitHub credential", regexp.MustCompile(`(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})`)},
	{"embedded AWS access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{"embedded JWT", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\.[A-Za-z0-9_-]{20,}\b`)},
	{"private local user path", regexp.MustCompile(`(?i)\bC:[/\\]+Users[/\\]+`)},
}

func inspect(name string, data []byte) []string {
	var failures []string
	for _, part := range strings.Split(name, "/") {
		if privateDirectory[strings.ToLower(part)] {
			failures = append(failures, "private runtime directory")
			break
		}
	}
	if !allowedSuffix[strings.ToLower(path.Ext(name))] && !allowedName[path.Base(name)] {
		failures = append(failures, "file type outside the publication allowlist")
	}
	if strings.HasSuffix(strings.ToLower(name), ".local.json") || path.Base(name) == ".env" {
		failures = append(failures, "local configuration")
	}
	if len(data) > 2_000_000 {
		failures = append(failures, "oversized source/documentation file requires review")
	}
	if !utf8.Valid(data) {
		return append(failures, "non-UTF-8 content")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		failures = append(failures, "binary content")
	}
	for _, p := range prohibited {
		if p.pattern.Match(data) {
			failures = append(failures, p.label)
		}
	}
	return failures
}

func git(args ...string) ([]byte, error) { return exec.Command("git", args...).Output() }

func run() error {
	listing, err := git("ls-files", "-z")
	if err != nil {
		return fmt.Errorf("cannot inspect Git index")
	}
	failed, count := false, 0
	for _, raw := range bytes.Split(listing, []byte{0}) {
		if len(raw) == 0 {
			continue
		}
		name := string(raw)
		count++
		data, err := git("show", ":"+name)
		if err != nil {
			return fmt.Errorf("cannot read staged file %q", name)
		}
		for _, label := range inspect(name, data) {
			fmt.Fprintf(os.Stderr, "FAIL: %q: %s\n", name, label)
			failed = true
		}
	}
	if failed {
		return fmt.Errorf("publication content check failed")
	}
	fmt.Printf("Publication content check passed for %d indexed files. Human provenance review is still required.\n", count)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
