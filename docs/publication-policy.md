# Publication policy

Follow [network address privacy](privacy.md): operator PC/console/residential endpoints belong in private configuration. Publish placeholders or synthetic fixtures, never addresses copied from the owner's network.

This repository publishes original server source, tests and documentation with recorded provenance. Retain the root PolyForm Shield license, per-server MIT notices and dependency attribution. A license grants only the rights held by its licensor; it does not grant rights to third-party games or assets.

## Content boundary

Do not commit game archives, ROMs, extracted executable or asset data, firmware, console keys, patches, emulator binaries, account profiles, signing material or raw network/gameplay captures. Do not include downloads or mirrors for such material. Keep screenshots containing commercial game artwork and private lab recordings outside the repository. Use original prose and structural protocol tests for evidence.

NES, Game Boy and N64 lab sources were imported through explicit source allowlists. The Nextendo-integrated N64 import also includes its existing generated protobuf Go types and a placeholder environment template. Manifests record input/output hashes and publication edits. Runtime profiles, patch generators, IPS files and captures were not imported. N64 deployment acceptance and client stability remain under investigation; the original source and license boundaries are preserved.

## Review before publication

1. Identify the original author, source and applicable license of every contribution.
2. Inspect the complete staged diff and dependency notices.
3. Stage only intended redistributable source and documentation.
4. Run `go run scripts/check-publication.go` and the affected Go checks.
5. Submit a pull request with the actual test scope and unresolved issues.

The automated check inspects Git's indexed file set, rejects binary/non-text material, prohibited artifact types, private runtime directories and common embedded credential formats. Synthetic test identities, loopback addresses and private-network fixtures are not live account credentials. The check cannot determine authorship, all secrets, infringement or the legal character of source code; human review remains necessary.

## Copyright and notices

Nintendo and other product names identify tested applications; their trademarks remain with their owners. No Nintendo affiliation or endorsement is claimed. A disclaimer, licence or passing CI cannot guarantee immunity from a DMCA complaint. GitHub's [DMCA policy](https://docs.github.com/en/site-policy/content-removal-policies/dmca-takedown-policy) covers copyright and technical-circumvention claims; removing game files alone does not decide those questions.

For an identified publication problem, report the affected repository paths through GitHub's private security reporting when available, or contact a repository maintainer without posting keys or private data. Maintainers should assess the specific issue and preserve necessary evidence privately. Do not disguise material or change attribution to evade a notice.
