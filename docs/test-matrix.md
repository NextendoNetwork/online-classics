# Manual test coverage

Owner-reported local results; these are not automated gameplay certification.

| Pairing | Genesis | GBA 3.3.0 | SNES 6.0.0 |
| --- | --- | --- | --- |
| Ryujinx / Ryujinx | Historical tests; fresh verification required for this import | Confirmed | Confirmed |
| Ryujinx / Switch | Fresh verification required for this import | Both host directions confirmed | Pairing confirmed |
| Citron / Ryujinx | Freeze observed around suspension; unresolved | Both host directions confirmed | Pairing confirmed |
| Citron / Switch | Not certified | Not separately tested | Not separately tested |
| Switch / Switch | Not certified | Not separately tested | Not separately tested |
| Different networks | Pending | Pending | Pending |

GBA testing included discovery, gameplay, leave/rejoin and host reversal. A Switch-host recovery run combined a friend-cache client change, corrected routing and a backend experiment; it does not isolate one change as the cause. SNES testing included two-player Killer Instinct and suspension. Exact durations and every action were not independently recorded. Four-player support is not claimed.

Record application version/build ID, backend revision, client revisions, optional flags, topology, host/guest roles, selected game, duration and each failed stage when extending this matrix. Automated Go tests exercise server behavior, not the commercial game or physical console.
