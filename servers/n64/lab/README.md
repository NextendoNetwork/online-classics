# N64 lab reference backend

This is the owner-tested, Genesis-derived N64 4.2.0 Go snapshot. It preserves the original project's [MIT license](LICENSE.md); it is distinct from the Nextendo-integrated PolyForm Shield module one directory above.

The owner reported successful Citron/Citron room behavior and then successful Citron/Switch gameplay using the local fixes. Exact duration, every game, reverse-host coverage and four-player acceptance were not independently recorded for the latest Switch report. Ryujinx N64 crashes remain an unresolved client issue.

```sh
go test ./... -timeout 60s
go vet ./...
go build -o server .
./server -log-file "" -session-token-profile npln-gss
```

Use `./server -help` for listeners and explicit lab identity pairing. Supply your own authorized test identities and client destinations; private lab credentials are not included. Run one backend at a time when sharing ports. This lab pairing mechanism is not production account verification.

The snapshot includes observed LCLA6/4P matchmaking configuration and exact console/version aliases, capacity checks and closed/inactive room filtering. Its test suite includes N64 discovery and lifecycle regressions. Publication-only edits replace historical LAN test addresses with synthetic fixtures. Client/game patches and raw captures remain private.

Credits: SoulToxic3119 for lab implementation, integration and manual testing; Nextendo Network for client/platform references; Codex for investigation and consolidation assistance. The [source manifest](../../../provenance-n64.json) records selected input hashes and exported files.
