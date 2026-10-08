# Manual test coverage

Owner-reported local results; these are not automated gameplay certification.

The separate [October 8 VPS record](vps-testing-2026-10-08.md) reports automated Linux tests for all six modules. Those passes do not add confirmed gameplay pairings to this matrix.

| Pairing | Genesis | GBA 3.3.0 | SNES 6.0.0 | NES 9.1.0 | Game Boy 4.1.0 |
| --- | --- | --- | --- | --- | --- |
| Ryujinx / Ryujinx | Historical tests; fresh verification required for this import | Confirmed | Confirmed | Both host directions confirmed | Confirmed |
| Ryujinx / Switch | Fresh verification required for this import | Both host directions confirmed | Pairing confirmed | Pairing confirmed | Both host directions confirmed |
| Citron / Ryujinx | Freeze observed around suspension; unresolved | Both host directions confirmed | Pairing confirmed | Both host directions confirmed | Both host directions confirmed |
| Citron / Switch | Not certified | Not separately tested | Not separately tested | Not separately tested | Not separately tested |
| Switch / Switch | Not certified | Not separately tested | Not separately tested | Not separately tested | Not separately tested |
| Different networks | Pending | Pending | Pending | Pending | Pending |

GBA testing included discovery, gameplay, leave/rejoin and host reversal. A Switch-host recovery run combined a friend-cache client change, corrected routing and a backend experiment; it does not isolate one change as the cause. SNES testing included two-player Killer Instinct and suspension. Exact durations and every action were not independently recorded. Four-player support is not claimed.

Record application version/build ID, backend revision, client revisions, optional flags, topology, host/guest roles, selected game, duration and each failed stage when extending this matrix. Automated Go tests exercise server behavior, not the commercial game or physical console.

NES and Game Boy results were reported by the owner during the local acceptance sequence: discovery, joining, gameplay, leaving/rejoining and host reversal. The owner reported no observed errors in the final tested pairings. Exact duration and every bundled game were not independently recorded. Game Boy Switch testing initially used base version 1.0.0, which did not match the prepared 4.1.0 setup; confirm the installed version before reproducing the final test. These reports do not certify every game in a catalogue.

N64 has a separate [stage-by-stage report](n64-testing.md): the owner reported Citron/Switch gameplay with local fixes after the earlier tests. Ryujinx gameplay stability, four-account testing and manual acceptance of the consolidated Nextendo account-integrated backend remain unresolved. Its source is now included without treating the lab result as integrated-service certification.
