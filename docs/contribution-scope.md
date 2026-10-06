# Contribution scope

This initial contribution collects independently developed Go lab backends and their automated tests. It does not bundle Nintendo software or experimental binary patches.

## Provenance

- Genesis: SoulToxic3119/genesis-server-docs, the English Go server snapshot derived from Soul Genesis Lab.
- GBA: SoulToxic3119/gba-nextendo-lab.
- SNES: SoulToxic3119/snes-nextendo-lab.
- NES and Game Boy: owner-maintained local labs derived from the Genesis baseline, exported on October 5, 2026. Source and exported hashes, file selection and transformations are recorded in provenance-nes-gb.json. No source Git revision is asserted for these private snapshots.

Exact source revisions and copied-file checksums are recorded in provenance.json. Local uncommitted differences, if present, are identified there. The three directories intentionally preserve their existing Go module declarations and tests for initial review.

The recorded hashes describe the original import. After import, Genesis doc.go and CREDITS.md were updated to point to this repository's documentation/provenance, a Genesis third-party notice was added, and the GBA third-party notice was adjusted to this server-only layout. Protocol implementation and test logic were not changed.

## License boundaries

The upstream root LICENSE.md is retained without modification. Each imported server directory includes its existing MIT license for original project material. Those notices are preserved rather than treating imported code as newly authored under the root license. Go dependencies retain their own licenses. Maintainers should confirm the intended mixed-license layout before merging.

Copyright notices on original code must remain. The exclusion policy concerns third-party game content and other material without redistribution permission; it does not mean removing authorship or license notices from legitimate source code.

The NES and Game Boy imports preserve the same per-server MIT attribution and root PolyForm Shield boundary. Publication-only edits update command/credit references and replace historical LAN addresses in tests with synthetic private-network fixtures. They do not alter server protocol behavior.

## Excluded material

Game archives, extracted executables/assets, firmware, console keys, IPS patches, emulator binaries, account caches, credentials, private addresses and raw gameplay/network logs are outside this contribution. No emulator or Prelude source trees are imported. The source directories are not copies of the private development archives.

N64 and SpeedRunners 2 are excluded from this update. See publication-policy.md for the repository content gate and its limitations.
