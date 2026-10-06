# Nintendo 64 Classics — Nextendo integration backend

NPLN server for Nintendo 64 Classics 4.2.0, application `0100C9A00ECE6000`, tenant `t-7b4e32ca-lp1`.

This module consolidates the Nextendo standalone N64 implementation at revision `10fc1a0` under its unchanged [PolyForm Shield license](LICENSE.md); its original repository identity and source hashes are recorded in the [provenance manifest](../../provenance-n64.json). Its account-service integration, protobuf service types, Gamesync, friends, messaging, NNCS, STUN/TURN and observability are retained. The separately tested Genesis-derived backend is preserved in [lab/](lab/README.md), with its MIT attribution. They are independent executables, not interchangeable deployment configurations.

## Build and configuration

```sh
go test ./... -timeout 60s
go vet ./...
go build -o server .
```

Use Go 1.27.1 or newer for the consolidated repository. Review [example.env](example.env) and supply your own account-service configuration, certificates, signing material and relay credentials through your deployment's secret management. Go does not automatically load this file as environment variables. Defaults describe local development, not a reachable public service.

Advertised STUN/TURN destinations must be reachable client addresses, not wildcard bind addresses. TURN relay sockets use OS-assigned UDP ports; the relay bind/advertisement must use an address assigned to the server. Follow the original endpoint distinctions in example.env and validate relay traffic on the deployment network.

## Consolidation changes

- N64/Classics search aliases accept only observed LCLA6 to LCLA6-2P/LCLA6-4P mappings, preventing unrelated prefix matches.
- Property lookup recognizes the two observed console/version aliases, preserves exact keys and requires matching values.
- For Classics rooms, explicit departure of the creating user terminates discoverability; guest departure keeps the host room available. A terminated room is rejected by membership lookup. Player-position changes do not redefine the creating user.
- Regression tests cover these boundaries. Existing service tests are retained.

These changes port behavior and lessons from the local lab; automated regression results are not a community deployment acceptance report. The owner's successful Citron/Switch gameplay report concerns the **lab** backend. Test this Nextendo-integrated module with the real account service before community rollout. Stable Ryujinx N64 gameplay remains unresolved independently of the server.

## Acceptance before community testing

1. Use distinct authorized accounts, exact client revisions and the matching 4.2.0 setup.
2. Repeat discovery, joining and gameplay with Citron/Switch in both host directions.
3. Verify leave/rejoin, host exit, room recreation and player-position changes without duplicate entries.
4. Validate four distinct players, full-room rejection, relay connectivity and separate-network behavior.
5. Confirm account authorization and keep diagnostic captures private.

See [test status](../../docs/n64-testing.md), [consolidation and migration](../../docs/n64-integration.md), and [publication policy](../../docs/publication-policy.md). No game data, client patches, patch generators or binaries are imported. The original Wonder client-patches directory is intentionally excluded because it is not N64 server source.
