# Account and transport staging, October 8, 2026

## Implemented in Go

Genesis, GBA, SNES, NES and Game Boy now default to `-deployment nextendo`.
They require private configuration, persistent P-256 signing material, a matching TLS
certificate, an operator-trusted RS256 BAAS JWKS, exact issuer/audience/title scope,
the account profile authority, and `/internal/online-check`.
There is no unsigned-token, source-IP identity, or automatic development fallback in
this mode. All eligible accounts can enroll; no subject allowlist is configured.

The shared [account verifier](../shared/accountauth) verifies the signed outer
credential before consulting the account authority. `/internal/npln-friends`
supplies canonical account and friend identities. Internal URLs and keys come only
from the operator. The account authority must implement these routes and provide
the trusted signing-key-to-device mapping; a supplied token cannot choose a URL.
BAAS credentials must include the signed `app_id` of the selected application.

NPLN access credentials last 15 minutes. Server-held sessions are bounded to 4096,
with a 24-hour renewal window and hashed, single-use refresh credentials. Renewal
keeps existing room credentials alive until their existing session expires.
Account eligibility and the online gate are rechecked for authenticated RPCs and
periodically on Gamesync streams. Gamesync tokens resolve to the account session
which created or joined their room, rather than supplying their own identity.

STUN advertises a public destination separately from its bind interface. TURN uses
fresh credentials issued only after account authentication, rechecks the account,
rejects access to private network peers, and allocates only inside the configured
UDP relay port range. The range contains at most 256 ports. Test mode permits only
literal loopback advertisement/listeners; it is not a public deployment profile.

The five-module service has no staging timer, closes on SIGINT/SIGTERM, limits
concurrent requests/streams, and has a [supervisor template](../deployment/online-classics@.service)
with memory/task/file limits. Those process limits are a containment measure;
they do not establish completed soak testing. The five modules also cap the room registry at 128, pending tickets at 512 and each issued match/Gamesync token registry at 4096. A minute sweep removes expired credentials and closed/abandoned rooms without active Gamesync channels; active rooms survive cleanup.

N64 now rejects its historical development identity fallback in Nextendo mode.
Its login uses the same signed-account verifier and gate. It also issues fresh
account-bound TURN credentials and uses separate bind/advertised relay addresses
with a bounded port range. Its different protocol implementation remains separately
reviewable; these changes do not certify N64 emulator stability or NNCS reachability.

## Commands

Build a complete checkout, including `shared/accountauth`; copying just one server
folder omits its local Go module dependency.

```sh
cd servers/gba
go build -o server .
./server -deployment nextendo -config /etc/online-classics/gba/config.json
```

Start from [config.example.json](../deployment/config.example.json). Empty fields
are intentional and must be provisioned privately. Change the title ID for each
module; the server rejects another application's scope. Only `deployment` and
`config` flags are accepted in this mode. Do not mix it with `lab-friend-*` flags.

The supervisor template requires an operator-provisioned `online-classics` service
user and group. Give that user read access to its configuration, signing key and
TLS key through restricted ownership/group permissions; do not make private keys
world-readable. Provisioning and enabling the service require operator review.

| Module | Title ID |
| --- | --- |
| Genesis | `0100B3C014BDA000` |
| GBA | `010012F017576000` |
| SNES | `01008D300C50C000` |
| NES | `0100D870045B6000` |
| Game Boy | `0100C62011050000` |
| N64 | `0100C9A00ECE6000` |

N64 retains environment configuration. `NPLN_ACCOUNT_CONFIG` points to a private
JSON containing the `account` object alone, with its N64 title scope. Configure
`NPLN_TURN_BIND_IP`, `NPLN_TURN_RELAY_MIN_PORT` and `NPLN_TURN_RELAY_MAX_PORT`.
The historical development path must explicitly set `NPLN_DEPLOYMENT=development`.
The other five modules require `-deployment development` to reproduce old flags.

## Acceptance still required

The automated fixtures test signed accounts, same-IP identity separation,
revocation, refresh replay rejection, account-bound Gamesync access, and actual
TURN allocations with issued versus unissued/revoked credentials. They do not use
production accounts and do not launch a game.

Before deployment, test the exact current binaries with each applicable emulator,
in both host directions, then with Prelude/Switch. Verify room discovery, gameplay,
leave/rejoin, five-minute AFK and recreation. Repeat on separate networks through
the operator's forwarded listeners and relay range. Preserve historical gameplay
results as historical; they used different authentication modes.

Additional integration work includes:

- Confirming that each real client supplies the enrolled signed BAAS credential
  and compatible NSA/friend identities; do not replace rejection with unsigned auth.
- Registering each Classics presence endpoint with the account service's online
  presence aggregation and proving cross-platform exclusion and release in vivo.
  The five modules expose an authenticated **loopback-only** `/api/stats` listener
  configured through `statsListen` and `statsKeyEnv` (private key, at least 32 bytes).
  It reports canonical account PIDs with actual stream-read inactivity and removes
  accounts when their last Gamesync/presence stream closes. The handler reads an
  immutable snapshot to avoid a circular wait while `/internal/online-check` polls it.
  Use an operator-reviewed `DASH_CLASSICS_URLS` hook in the isolated account service,
  or the maintainer's equivalent integration; this is not applied to production here.
- A service soak test covering repeated creation, abandonment, refresh and reconnect,
  verifying the implemented room/ticket quotas and cleanup under load. Automated
  capacity/reclamation tests and process memory limits alone do not certify a soak.
- N64 NNCS still requires two assigned public IPv4 addresses under its retained
  deployment profile. The testing host's router/NAT setup does not provide this.
- Client-specific Gamesync/presence/messaging behavior and N64 Ryujinx stability.

The branch remains staging work until these checks pass. No production Nextendo
service is changed or restarted by this repository update. No personal addresses,
keys, raw client captures, ROMs, Unity DLLs or emulator binaries are published.
