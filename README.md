# Online Classics

Experimental Go server implementations for Nintendo Switch Online Classics, contributed for Nextendo maintainer review.

## Implementations

| Server | Reference application version | Source directory |
| --- | --- | --- |
| SEGA Genesis | 3.1.1 | [servers/genesis](servers/genesis) |
| Game Boy Advance | 3.3.0 | [servers/gba](servers/gba) |
| Super Nintendo Entertainment System | 6.0.0 | [servers/snes](servers/snes) |

These are independent review snapshots of related lab implementations. They are not yet a single multi-title production service. Run one backend at a time when using the same ports. Deduplicating shared handlers is a subsequent integration step, after preserving the behavioral differences and tests.

## Build

Use Go 1.27.1 or newer. From one server directory:

```sh
go test ./... -timeout 60s
go vet ./...
go build -o server .
```

Review command-line options with `go run . -help`. Lab identity pairing and generated signing/TLS material require production integration with Nextendo's account, authorization and key-management services before deployment.

## Review documentation

- [Scope, provenance and licensing](docs/contribution-scope.md)
- [Manual test coverage](docs/test-matrix.md)
- [Maintainer integration checklist](docs/integration.md)
- [Source architecture and title-specific behavior](docs/architecture.md)
- [Automated import validation](docs/validation.md)

No game archives, extracted game data, firmware, system keys, IPS binaries, emulator binaries, account profiles or raw captures are included. Client patch installation and emulator fixes are separate from these Go backends.

The existing root license is retained. Imported server directories retain their original MIT license notices; see the scope document for the per-directory boundary. Dependencies retain their respective licenses.
