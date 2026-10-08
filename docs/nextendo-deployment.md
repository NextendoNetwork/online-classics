# Nextendo server deployment

Deployment targets Nextendo-operated servers. Client destinations must come from the operator's public configuration, never the original test PC or console LAN addresses. This repository does not assign a Nextendo domain/IP or claim an existing deployment.

## Module readiness

| Module | Deployment path | Remaining work |
| --- | --- | --- |
| N64 (`servers/n64`) | Account-integrated module, explicit VPS configuration and startup validation | Maintainer account review, staging gameplay and separate-network/relay acceptance |
| Genesis, GBA, SNES, NES, Game Boy | Default Nextendo mode, shared signed-account verifier, account graph, fresh authenticated TURN and separate bind/public destinations | Current-client credentials, online presence integration, lifecycle quotas/soak and gameplay acceptance |
| N64 historical reference (`servers/n64/lab`) | Reproduction source with separate MIT attribution | Excluded from the Nextendo deployment path |

For the five shared-baseline modules, follow the [account/transport staging guide](account-transport-staging.md) and [private configuration template](../deployment/config.example.json). Nextendo mode rejects development flags and incomplete account/key/network provisioning. Historical local gameplay results do not certify the new mode.

## N64 VPS configuration

N64 also requires `NPLN_ACCOUNT_CONFIG`: a private JSON containing the account object from the common template, with its N64 title ID, trusted JWKS, issuer/audience, verified profile authority, mandatory online gate and signing-key device mapping. Missing provisioning rejects startup.

Provision [example.env](../servers/n64/example.env) through the deployment's secret management. Go does not load this file automatically. `NPLN_DEPLOYMENT=nextendo` is the default; incomplete configuration stops startup. Historical reproduction must explicitly select `NPLN_DEPLOYMENT=development`.

- Supply public IPv4 addresses for Gamesession, latency, STUN, TURN and NNCS. Loopback, LAN, wildcard, CGNAT and documentation addresses are rejected as client destinations. This deployment profile requires IPv4 literals and does not resolve DNS.
- Listener interfaces and advertised destinations are separate. `0.0.0.0` can be a listener but cannot be advertised to clients.
- NNCS requires two distinct public IPv4 addresses assigned to the host. TURN now separates its bind interface (`NPLN_TURN_BIND_IP`) from the advertised relay address, with `NPLN_TURN_RELAY_MIN_PORT`/`NPLN_TURN_RELAY_MAX_PORT`. NNCS still needs its two assigned public addresses.
- Set the internal account service URL explicitly. The shared verifier requires HTTPS, except for literal loopback HTTP in an isolated test. Internal account addresses are not game-client destinations.
- Provision persistent TLS material and the P-256 NPLN signing key. Startup checks the matching TLS pair/profile and key format. Invalid deployment material fails instead of falling back to generated keys. Manage certificate renewal outside the running service.
- Supply Nextendo proof, internal account and TURN credentials privately. Leave `NPLN_ALLOW_UNVERIFIED` absent/empty: even `0` enables the retained bypass. Legacy signing and forced certificate regeneration are rejected in deployment mode.

Empty template fields intentionally contain no invented public endpoint or credential. Provision operator-approved values before startup; keep secrets, account data and captures outside Git.

## Network and acceptance

Review TLS routing/client trust with Prelude and emulator maintainers. Expose the configured TCP listener and UDP STUN/TURN listeners, plus NNCS ports defined in `servers/n64/nncs.go`. TURN now allocates inside its configured bounded relay range; forward that range along with the STUN/TURN listeners. Its fresh credentials are issued only after the signed account gate; static development credentials are not accepted in Nextendo mode.

Repeat discovery, join, gameplay, host reversal, leave/rejoin, timed AFK and room recreation using distinct Nextendo accounts on separate networks. Verify invalid/revoked credential rejection and authenticated relay access. Listener startup does not certify public reachability, authorization or gameplay. See [integration](integration.md) and [N64 acceptance](n64-testing.md).

Record the revision, private operator configuration, OS, service definition, resource limits, health checks, restart policy and rollback. Keep the service in staging until the maintainer accepts account and relay policy. Work uses a separately authorized testing VPS. Production Nextendo servers are not modified or restarted.
