# Signed-account Go verification — October 8, 2026

Source revision: `c0f21bd325a2e04e81aa99aadcdab0152fc997aa` on
`codex/nextendo-deployment`. This run replaces neither historical gameplay
results nor the earlier automated run at `d15c7e5`.

## Completed checks

- Native Windows tests and `go vet ./...` passed for the shared account verifier,
  publication checker, all six title modules and the separate N64 lab module.
- `go test -race ./...` passed for the shared verifier, GBA and N64. GBA exercises
  the common five-module account/session implementation; this does not claim
  that every module was individually run with the race detector.
- All six current root-package Linux test executables passed on the isolated
  testing VPS, with a 90-second timeout per executable.
- Linux service and test executables were built with Go 1.27.1, Linux/amd64,
  CGO disabled. All twelve remote SHA-256 values matched the local artifacts.
- The indexed publication checker passed. No Python or C# implementation is
  present in the active public server source.

The new suites include signed title-scoped account login, canonical same-IP
identity separation, authority friend identities, mandatory gate/revocation,
Gamesync session binding, issued TURN credentials and real local TURN allocations.
The five shared-baseline modules also test refresh replay rejection, immutable
presence snapshots and bounded room cleanup. N64 has its own protocol and tests;
its refresh and lifecycle behavior must be reviewed separately.

## Artifact fingerprints

| Module | Service SHA-256 | Test executable SHA-256 | Linux suite |
| --- | --- | --- | --- |
| genesis | `3e0b67f6e0c28e0ffb9fd59d184e28d95da85c73eede3f8b6091c47da4a665a7` | `fc1ac0ebcfc1f59f0dfaa19061059894d9db925eb753feb0280d1c0750d763e5` | PASS |
| gba | `55a7950059f83f8b2132fad3670ff6b8381076b6c1abf24e79cc76d7135e63b9` | `652b8315f9b5b784dfc44a82635397fcdb686cad9c894c63817f5736c628422a` | PASS |
| snes | `367e9f27bd5648c66d28f8663c1c6b75114d9f1a16c55833e103c508ec804b4d` | `b77e31531e410ed7425aae432d31ffaa4ee085a9b9474c4e6959ba43f82e5aa8` | PASS |
| nes | `c1c2ddbd2c714c71956600b65de677e38ba97bc3b303b0b573c395086c43854e` | `15ccf34521337cc1954f9beed4f886883a3da7c7497f3a3e14ea01bbe899f50f` | PASS |
| gb | `c687edba79b85a590b9a68938a98d10220d576a1546b5da42f1d95407a16875a` | `ffffb1eb9995c810ef595e3163c1fc6d6a0188204074e54fea354aa90ec89f52` | PASS |
| n64 | `6db007e43319eb621cb63e93f7910ff3603ef04c3f33f1760a31c66126f84597` | `45b54242a9ed0d03ebad39c35300847e497b5bb6a7f43e4a119402a478596341` | PASS |

The service executables were compiled and fingerprinted. This run executed the
**test executables**, not new public game services. Tests used fictitious accounts,
signed fixtures and loopback authority/listeners; they did not use a production
Nextendo account or launch an emulator. Private logs and executables are retained
outside this repository.

## Still pending

Follow the [account/transport acceptance guide](account-transport-staging.md).
Ryujinx, Citron and Prelude/Switch must repeat discovery, joining, gameplay,
host reversal, leave/rejoin, five-minute AFK and room recreation against the
current account-integrated mode. Router forwarding, real client credentials,
account-service presence integration and a service soak remain pending.
N64 NNCS additionally needs its retained two-public-address requirements resolved
for this test host. No production server was modified or restarted.