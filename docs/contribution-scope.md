# Contribution scope

This initial contribution collects independently developed Go lab backends and their automated tests. It does not bundle Nintendo software or experimental binary patches.

## Provenance

- Genesis: SoulToxic3119/genesis-server-docs, the English Go server snapshot derived from Soul Genesis Lab.
- GBA: SoulToxic3119/gba-nextendo-lab.
- SNES: SoulToxic3119/snes-nextendo-lab.

Exact source revisions and copied-file checksums are recorded in provenance.json. Local uncommitted differences, if present, are identified there. The three directories intentionally preserve their existing Go module declarations and tests for initial review.

## License boundaries

The upstream root LICENSE.md is retained without modification. Each imported server directory includes its existing MIT license for original project material. Those notices are preserved rather than treating imported code as newly authored under the root license. Go dependencies retain their own licenses. Maintainers should confirm the intended mixed-license layout before merging.

Copyright notices on original code must remain. The exclusion policy concerns third-party game content and other material without redistribution permission; it does not mean removing authorship or license notices from legitimate source code.

## Excluded material

Game archives, extracted executables/assets, firmware, console keys, IPS patches, emulator binaries, account caches, credentials, private addresses and raw gameplay/network logs are outside this contribution. No emulator or Prelude source trees are imported. The source directories are not copies of the private development archives.
