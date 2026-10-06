# N64 consolidation and community rollout

## Layout and license boundaries

| Path | Source | License | Evidence |
| --- | --- | --- | --- |
| servers/n64 | Nextendo standalone N64 repository, revision recorded in provenance-n64.json | Original PolyForm Shield 1.0.0 retained | Existing automated tests plus scoped compatibility/lifecycle regressions; deployment acceptance pending |
| servers/n64/lab | Owner-maintained Genesis-derived N64 lab snapshot | Original Genesis MIT attribution preserved | Owner-reported local Citron/Citron and Citron/Switch results; automated checks |

The implementations use different authentication and service structures. Do not replace the account-integrated service with the lab identity pairing mechanism for production. Do not report the lab gameplay result as acceptance of the newly integrated service.

The import selects Go implementation/tests, module manifests, generated protobuf Go types, the original N64 license and a configuration template with placeholders. It excludes the unrelated Wonder client-patches directory, executable outputs and private profiles/captures. Upstream sources and dependency notices retain their attribution; imported files are not relicensed as MIT.

## Compatibility fixes

The integration restricts Classics configuration aliases to observed room types, recognizes only the observed console/version property aliases, preserves exact property keys, and closes Classics rooms when their creating user explicitly departs. Membership lookup rejects terminated rooms. Guest exit remains distinct from host exit, and swapping player positions is not treated as creating a new owner.

These are scoped changes with regression tests. Abrupt disconnect/reconnect, stale room expiry, host exit during gameplay and every player-position transition still require deployment tests; no general host-migration support is claimed. Four player slots are supported by the room contract, but four-account gameplay has not been validated.

## Migration

1. Deploy a build from servers/n64 in a staging environment with the Nextendo account service and explicit connectivity configuration.
2. Repeat the acceptance sequence in its README and docs/n64-testing.md, preserving backend/client revisions and failed stages.
3. Compare the staging result against the lab snapshot before attributing differences to a server or client change.
4. Preserve the old repository's Git history, license, references and any service configuration outside public source control.
5. Update operator links to this repository after staging acceptance. Repository consolidation does not itself deploy a service or change clients' DNS.

Removing the standalone GitHub repository is a separate destructive action. It removes its repository URL and associated GitHub metadata; a Git bundle preserves Git objects, not every GitHub feature. Keep a migration notice or archived repository until the owner decides whether deletion is appropriate.
