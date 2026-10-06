# Classics community staging checklist

Local manual success is the starting evidence for staging, not acceptance of a new public deployment. Capture the backend commit and exact client versions for each title separately.

| Module | Integration work before rollout |
| --- | --- |
| Genesis, GBA, SNES, NES, Game Boy | Replace lab identity pairing with trusted Nextendo account verification; configure persistent signing/TLS material and authorized connectivity services |
| N64 account-integrated service | Validate the retained Nextendo account-service contract and credentials; repeat gameplay after the scoped discovery/lifecycle changes |
| N64 lab reference | Use as a local reproduction baseline; do not use its identity pairing as production account verification |

## Operator acceptance

1. Start one title in staging with operator-supplied account endpoints, certificates and signing secrets. Keep private configuration outside Git.
2. Confirm authentication and room access for authorized users, and rejection for invalid or unrelated identities. Do not expose the lab simulation endpoints as production account services.
3. Verify the application/tenant routing reaches the selected backend. Shared tenant names do not mean all title backends can bind the same address and ports independently.
4. Advertise reachable STUN/TURN endpoints and relay destinations. Validate direct and relay traffic between clients on different networks, including required UDP relay ports.
5. Repeat room discovery, join, gameplay, pause/resume, leave/rejoin, host exit, fresh room creation and reverse-host tests on each intended client pairing.
6. For N64, validate four distinct online accounts separately; two-player gameplay does not certify four-player operation.
7. Set operational limits, room expiration policy, persistent-key handling and logging access before inviting community testers. Keep tokens and identifying captures private.
8. Document the result and preserve a previous working deployment for rollback. Start community testing with the configurations actually accepted in staging.

Known client failures remain visible in [test-matrix.md](test-matrix.md) and [n64-testing.md](n64-testing.md). No production deployment is performed by merging source into this repository.
