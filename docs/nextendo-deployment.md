# Nextendo server deployment

Deployment targets Nextendo-operated servers. Client destinations must come from the operator's public configuration, never the original test PC or console LAN addresses. This repository does not assign a Nextendo domain/IP or claim an existing deployment.

## Module readiness

| Module | Deployment path | Remaining work |
| --- | --- | --- |
| N64 (`servers/n64`) | Account-integrated module, explicit VPS configuration and startup validation | Maintainer account review, staging gameplay and separate-network/relay acceptance |
| Genesis, GBA, SNES, NES, Game Boy | Protocol source and local acceptance evidence | Replace local identity pairing and subnet-restricted relays with maintained Nextendo authentication, authorization and public connectivity |
| N64 historical reference (`servers/n64/lab`) | Reproduction source with separate MIT attribution | Excluded from the Nextendo deployment path |

Substituting an IP does not implement account verification or public TURN authorization in the five reference modules. Their current executables are not the VPS rollout path. Preserve attribution and recorded test conditions during migration.

## N64 VPS configuration

Provision [example.env](../servers/n64/example.env) through the deployment's secret management. Go does not load this file automatically. `NPLN_DEPLOYMENT=nextendo` is the default; incomplete configuration stops startup. Historical reproduction must explicitly select `NPLN_DEPLOYMENT=development`.

- Supply public IPv4 addresses for Gamesession, latency, STUN, TURN and NNCS. Loopback, LAN, wildcard, CGNAT and documentation addresses are rejected as client destinations. This deployment profile requires IPv4 literals and does not resolve DNS.
- Listener interfaces and advertised destinations are separate. `0.0.0.0` can be a listener but cannot be advertised to clients.
- NNCS requires two distinct public IPv4 addresses assigned to the host. TURN also binds relay sockets directly to its advertised relay address. NATed containers/VPSs need transport changes before separate bind and relay-advertised addresses can be supported.
- Set the internal account service URL explicitly. Public traffic requires HTTPS; HTTP is accepted only for private/loopback literal addresses on an operator-controlled service hop. Internal account addresses are not game-client destinations.
- Provision persistent TLS material and the P-256 NPLN signing key. Startup checks the matching TLS pair/profile and key format. Invalid deployment material fails instead of falling back to generated keys. Manage certificate renewal outside the running service.
- Supply Nextendo proof, internal account and TURN credentials privately. Leave `NPLN_ALLOW_UNVERIFIED` absent/empty: even `0` enables the retained bypass. Legacy signing and forced certificate regeneration are rejected in deployment mode.

Empty template fields intentionally contain no invented public endpoint or credential. Provision operator-approved values before startup; keep secrets, account data and captures outside Git.

## Network and acceptance

Review TLS routing/client trust with Prelude and emulator maintainers. Expose the configured TCP listener and UDP STUN/TURN listeners, plus NNCS ports defined in `servers/n64/nncs.go`. TURN currently allocates OS-assigned UDP relay ports: review firewall exposure and allocation limits, or implement a bounded range before deployment.

Repeat discovery, join, gameplay, host reversal, leave/rejoin, timed AFK and room recreation using distinct Nextendo accounts on separate networks. Verify invalid/revoked credential rejection and authenticated relay access. Listener startup does not certify public reachability, authorization or gameplay. See [integration](integration.md) and [N64 acceptance](n64-testing.md).

Record the revision, private operator configuration, OS, service definition, resource limits, health checks, restart policy and rollback. Keep the service in staging until the maintainer accepts account and relay policy. No VPS has been modified by these repository changes.
