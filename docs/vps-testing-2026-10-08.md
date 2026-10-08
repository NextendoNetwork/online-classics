# Classics automated VPS test record — October 8, 2026

## Scope

This section preserves the historical automated run at `d15c7e5`. The current
signed-account implementation at `c0f21bd` has its own
[Linux verification record](signed-account-verification-2026-10-08.md).

The six title modules' root-package test executables passed on a separate Linux/amd64 test VPS. The tested source revision was `d15c7e5ac50daacd2548ce534c39af4dd40f6382` on `codex/nextendo-deployment`, with no module source changes for this run. The date uses the operator's client timezone.

The executables were cross-compiled with Go 1.27.1 on Windows/amd64, targeting Linux/amd64 with CGO disabled, then copied to a private test directory. Remote SHA-256 hashes matched the local artifacts. Each test executable ran with a 90-second test timeout and exited successfully; the retained private logs end in `PASS`.

## Results

| Application | Tested package | Linux result |
| --- | --- | --- |
| Nintendo 64 | `servers/n64` root package | PASS |
| SEGA Genesis | `servers/genesis` root package | PASS |
| Game Boy Advance | `servers/gba` root package | PASS |
| Super Nintendo | `servers/snes` root package | PASS |
| Nintendo Entertainment System | `servers/nes` root package | PASS |
| Game Boy | `servers/gb` root package | PASS |

This run did not include the separate `servers/n64/lab` module. Earlier local/CI checks are recorded in [validation](validation.md).

### Executable fingerprints

| Artifact | SHA-256 |
| --- | --- |
| `classics-n64.test` | `5c69ad8e4b8b87121e8f4a7618cbb50eae88fb1328f573d9514dca44ca463d52` |
| `classics-genesis.test` | `858e48074aaba96c768355f4b2a6688169ac610db3ccf519467f490260a7169a` |
| `classics-gba.test` | `a979f1cb56b070e155bb93734d91258b4d35947f28e69ba051c1c467cc4f9d19` |
| `classics-snes.test` | `9ee5e5c83066b73baadb249ac44bba912df03fa046ff7f449ce6a45cb1c562da` |
| `classics-nes.test` | `85c0e20878d862d47d19453b83afe7fb6ab281b9d7a9319430c44d576d92b7ff` |
| `classics-gb.test` | `038d4b4f8c45103166b2998e07f4f42ef330cae2e0667b2479af87904839c5a7` |

These fingerprints identify this run's executables; rebuilding elsewhere may produce different hashes. Executables and raw logs remain private and are not distributed by this repository.

## Reproduction

From each listed module, cross-compile its root-package test executable:

```powershell
$env:GOOS = 'linux'
$env:GOARCH = 'amd64'
$env:CGO_ENABLED = '0'
go test -c -o classics-TITLE.test .
```

Transfer the resulting file to an authorized test host, compare SHA-256, and run:

```sh
chmod 700 classics-TITLE.test
./classics-TITLE.test -test.timeout=90s
```

Use a new shell for subsequent native-platform Go work so the cross-compilation environment does not carry over. This procedure runs the existing tests, including their synthetic fixtures and local test listeners; it does not start a public game service.

## What remains pending

- Actual Ryujinx, Citron and Prelude/Switch sessions against these VPS builds: discovery, joining, gameplay, host reversal, leave/rejoin, AFK and room recreation.
- Live acceptance of the newly implemented account and relay paths for all six
  modules, as specified in the [deployment guide](nextendo-deployment.md#module-readiness).
- Router forwarding and reachable advertised relay destinations. N64's direct public-address assumptions still need resolution for this test host behind a router.
- Different-network, four-player and physical Switch/Switch acceptance.

The [manual coverage matrix](test-matrix.md) records historical local gameplay separately. The isolated UCH account/transport tests belong to the UCH repository and do not establish account integration for Classics. These six passes are automated Linux evidence, not VPS production readiness or new console gameplay results. No Nextendo production service was changed for this run.

Only source revision, toolchain, commands, outcomes and artifact hashes are published. Host addresses, SSH details, credentials, account profiles and raw captures remain private.
