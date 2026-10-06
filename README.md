<h1 align="center">online-classics</h1>

<p align="center">
  <b>Genesis, Game Boy Advance, SNES, NES and Game Boy server implementations for Nextendo Network.</b>
</p>

<p align="center">
  <a href="https://github.com/NextendoNetwork/online-classics/actions/workflows/go.yml"><img src="https://github.com/NextendoNetwork/online-classics/actions/workflows/go.yml/badge.svg" alt="Go server checks"></a>
  <img src="https://img.shields.io/badge/Go-1.27.1%2B-00ADD8" alt="Go 1.27.1 or newer">
  <a href="LICENSE.md"><img src="https://img.shields.io/badge/Root_license-PolyForm_Shield_1.0.0-orange" alt="Root license: PolyForm Shield 1.0.0"></a>
  <a href="docs/contribution-scope.md"><img src="https://img.shields.io/badge/Imported_servers-MIT-blue" alt="Imported server source: MIT"></a>
</p>

---

## What is this?

Experimental **Go** implementations of the NPLN/Gamesync services used by five Nintendo Switch Online Classics applications, contributed for integration with [Nextendo Network](https://nextendo.network).

The backends handle lab authentication, session discovery and joining, friends and presence, Gamesync documents and watches, signaling, and local STUN/TURN experiments. They derive from Soul Genesis Lab. Public NPLN references and Nextendo server examples informed the protocol investigation.

Each title currently has an independent module. Shared-handler consolidation and production account integration remain maintainer work. Run one backend at a time when using the same ports.

## Supported references

| Application | Version | Go implementation | Reported local results |
| --- | --- | --- | --- |
| SEGA Genesis | 3.1.1 | [servers/genesis](servers/genesis) | Further validation required; Citron suspension freeze unresolved |
| Game Boy Advance | 3.3.0 | [servers/gba](servers/gba) | Ryujinx/Ryujinx, Ryujinx/Switch and Citron/Ryujinx; both host directions tested |
| Super Nintendo Entertainment System | 6.0.0 | [servers/snes](servers/snes) | Ryujinx/Ryujinx, Ryujinx/Switch and Citron/Ryujinx |
| Nintendo Entertainment System | 9.1.0 | [servers/nes](servers/nes) | Ryujinx/Ryujinx, Citron/Ryujinx and Ryujinx/Switch; host reversal reported |
| Game Boy | 4.1.0 | [servers/gb](servers/gb) | Ryujinx/Ryujinx, Citron/Ryujinx and Ryujinx/Switch; host reversal reported |

These are owner-reported tests on a local network. Separate-network gameplay, four-player coverage, Switch/Switch and Citron/Switch remain unverified. See the [test matrix](docs/test-matrix.md) for the precise scope.

**N64 is excluded from this update.** Its room and gameplay compatibility tests are ongoing; Ryujinx game-load crashes remain under investigation. SpeedRunners 2 is also outside this repository's scope.

## Build and run

Use **Go 1.27.1 or newer**. Select a title module, then build it:

```sh
cd servers/gba
go build -o server .
go run . -log-file ""
```

Replace `gba` with `genesis`, `snes`, `nes` or `gb` as needed. Defaults use loopback listeners. Run `go run . -help` for configuration flags and review the [integration guide](docs/integration.md) before configuring real clients.

Temporary TLS/signing material and explicit local identity pairing support lab tests. Production use requires Nextendo account verification, persistent keys, permissions, reachable ICE/relay destinations and authorized relay access.

## Tests

From each title module:

```sh
go test ./... -timeout 60s
go vet ./...
go build -o server .
```

The initial three modules passed these checks locally and in GitHub's Linux workflow. NES and Game Boy extend the same workflow; see [validation](docs/validation.md) for the current source checks. Automated checks validate server behavior; gameplay needs the separate manual acceptance sequence.

## Clients

Tests used [Ryujinx-Nextendo](https://github.com/NextendoNetwork/Ryujinx-Nextendo), [Citron-Nextendo](https://github.com/NextendoNetwork/citron-nextendo), and a physical Switch. Clients need the matching application version, authorized patch setup, service routing, and an identity accepted by the backend.

[Prelude](https://github.com/NextendoNetwork/Prelude-Nro) preparation and emulator changes are maintained separately. See the [client setup guide](docs/client-setup.md) for installation and deployment requirements.

## Documentation

- [Architecture and title-specific behavior](docs/architecture.md)
- [Maintainer integration](docs/integration.md)
- [Client setup and test sequence](docs/client-setup.md)
- [Gameplay test matrix](docs/test-matrix.md)
- [Automated validation](docs/validation.md)
- [Source provenance and contribution scope](docs/contribution-scope.md)
- [Publication policy and content checks](docs/publication-policy.md)
- [Contributing](CONTRIBUTING.md)
- [Credits](CREDITS.md)

## Contributors and AI assistance

| Contributor | Work |
| --- | --- |
| [SoulToxic3119](https://github.com/SoulToxic3119) | Project direction, original lab implementation, integration and manual testing |
| [Nextendo Network](https://github.com/NextendoNetworkProfile) | Platform, upstream projects and maintainer coordination; [organization repositories](https://github.com/NextendoNetwork) |
| [OpenAI Codex](https://github.com/openai/codex) | Investigation, English documentation, repository organization and validation assistance |
| [Claude Code](https://github.com/anthropics/claude-code) | Earlier Genesis development and review assistance recorded in the imported project |

See [credits and provenance](CREDITS.md) for attribution scope. GitHub generates its sidebar Contributors list from commit history; this table also recognizes documented tool assistance.

## Included material

Original server source, tests, protocol implementation notes and documentation are included. Game archives, extracted game data, firmware, system keys, IPS binaries, emulator binaries, account profiles and raw captures are excluded. Nintendo trademarks identify the applications being tested; this project is not affiliated with Nintendo.

## License

The repository's root [PolyForm Shield License 1.0.0](LICENSE.md) is retained. Imported original server material preserves its **MIT** notices in [Genesis](servers/genesis/LICENSE.md), [GBA](servers/gba/LICENSE), [SNES](servers/snes/LICENSE), [NES](servers/nes/LICENSE.md), and [Game Boy](servers/gb/LICENSE.md). Dependencies retain their own licenses. See [license boundaries and provenance](docs/contribution-scope.md).
