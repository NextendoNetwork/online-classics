# Game Boy Classics backend

Application reference: Game Boy 4.1.0, ID 0100C62011050000, tenant t-7b4e32ca-lp1. Original source derives from the Genesis lab and preserves its MIT license.

```sh
go test ./... -timeout 60s
go vet ./...
go build -o server .
./server -log-file "" -session-token-profile npln-gss
```

Default TLS listener: 127.0.0.1:8443. Run `./server -help` for explicit listeners and local identity pairing. Pairing requires two distinct, authorized accounts and operator-supplied identities; do not copy private lab account configuration. Some inherited diagnostic flags and log prefixes retain Genesis names. They are not automatic Game Boy compatibility fixes.

Local owner reports cover Ryujinx/Ryujinx, Citron/Ryujinx and Ryujinx/Switch. Matching client routing, version-specific patch setup and separately maintained Ryujinx friend-resolution changes were required. Patches and client binaries are not distributed here. A 4.1.0 client setup does not apply to the base 1.0.0 installation. Review [client setup](../../docs/client-setup.md), [integration](../../docs/integration.md), and the [test matrix](../../docs/test-matrix.md) before extending the tests.
