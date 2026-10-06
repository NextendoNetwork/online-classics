# Nintendo 64 Classics: experimental test status

**Status: server consolidation and staged validation in progress. N64 backend source is included; no client build or game patch is included.**

Reference application: Nintendo 64 – Nintendo Classics 4.2.0, application ID `0100C9A00ECE6000`. Results below describe local tests reported by the owner and reviewed diagnostic traces as of October 5, 2026. They do not certify the catalogue or a public Nextendo deployment.

## Compatibility evidence

| Pairing / stage | Observed result | Remaining work |
| --- | --- | --- |
| Citron / Citron: room discovery and joining | Owner reported successful entry after the local discovery changes | Repeat lifecycle and gameplay acceptance with recorded game and duration |
| Citron / Citron: duplicate room listing | Owner reported that the phantom entry no longer appeared after the local fix | Repeat fresh creation, leave/rejoin and host reversal; production behavior is not established |
| Citron / physical Switch: gameplay | Owner subsequently reported successful gameplay with the local fixes | Exact duration/game and reverse-host coverage were not independently recorded; repeat on the account-integrated staging backend |
| Ryujinx / Citron: discovery and joining | Both host directions reached the shared room | Stable game loading and sustained gameplay remain unresolved |
| Ryujinx: Mario Kart 64 offline | Owner reported crashes in One Player mode too | Diagnose the game-load failure independently of matchmaking |
| Ryujinx: Super Mario 64 | Owner reported another crash during the comparison | Determine whether the shared failing path has the same underlying cause |
| Ryujinx / Ryujinx | Two isolated clients were launched for comparison | Gameplay outcome has not yet been reported |
| Four distinct online players | Not validated | Complete the four-player sequence below |

## Ryujinx investigation

The supplied upstream [DMA mapping fix](https://github.com/NextendoNetwork/Ryujinx-Nextendo/commit/9a4445d28270037879fad96eee87207e14ac9a6d) was incorporated into the private test build. It guards unmapped GPU copy ranges. Passing that startup path did not establish stable N64 gameplay.

Separate diagnostic runs exposed different fatal paths:

- A firmware JIT mutex assertion associated with the thread-local handle path.
- Invalid guest CPU memory reads around the shared game-launch/heap path.
- A Windows unwind callback whose delegate lifetime was insufficient; a lab candidate retained the callback per cache. Guest memory failures remained afterward.
- Invalid shader instructions and an out-of-range constant-buffer binding during a run that reached gameplay.
- A Vulkan `ErrorDeviceLost` exit after Mario Kart 64 loading.

Thread-local handle initialization, callback lifetime changes and additional CPU/GPU tracing remain private experimental candidates. They are not represented here as a completed repair or a released client fix. The collected shader failures may be symptoms of earlier corruption; their origin is unresolved. Reported audio distortion has not been independently attributed.

Sampled resource counters did not show physical RAM exhaustion or overall CPU saturation during the measured comparison. Sampling can miss short peaks, and high memory commit was observed. Those measurements do not exclude resource pressure, a driver issue or another cause. No single CPU, GPU, shader-cache or laptop-saturation diagnosis is confirmed.

## Reproduce with two clients

1. Record the application version, backend revision, client revision/build hash, host/guest roles, topology and configuration. Use two distinct authorized accounts with isolated profiles.
2. Close the previous comparison clients normally. Keep only the intended pair and the selected test backend running.
3. Start both clients with the same build and equivalent graphics, memory and cache settings. Record any diagnostic options; they can affect timing and do not become recommended production settings merely because a trace uses them.
4. Create a fresh room with player A. Discover and join with player B. Record this outcome separately from game loading.
5. Start Mario Kart 64 and record which client exits, the time relative to game selection, the last useful log messages and whether the peer continues running.
6. If a client fails, compare One Player offline loading and a second game using the same configuration. Change one setting at a time when testing a specific hypothesis.
7. After stable gameplay, test suspension/resume, returning to the catalogue, leaving/rejoining, room recreation and both host directions.

Keep logs, profiles, game dumps, shader captures and recordings private. Publish original written summaries that omit account IDs, tokens, local paths and live addresses. Use the same content boundaries as the [publication policy](publication-policy.md).

## Four-player acceptance

N64 exposes four player slots. Two accounts do not validate four-player online behavior.

1. Prepare four distinct authorized identities with isolated profiles or devices and a backend configured to accept all four. Avoid sharing account sessions.
2. Have A create one fresh room. Join B, C and D sequentially; verify the visible count progresses from 1/4 to 4/4 with one discoverable room entry.
3. Confirm each player's controller input in a game that actually supports four players, such as Mario Kart 64. Record the duration and client versions.
4. Test a full-room join rejection with an additional authorized test identity if available. Otherwise mark that case untested.
5. Have a guest leave and rejoin; verify occupancy and room discovery update correctly. Test normal host exit and document the resulting room lifecycle rather than assuming host migration exists.
6. Recreate the room under a different host. Test player-position changes separately from leaving and creating a new room, checking that they do not produce duplicate room entries.
7. Extend to mixed emulator/Switch pairings and separate networks only after local four-player gameplay is stable.

Record each discovery, join, input, lifecycle and reconnect outcome separately. The source consolidation preserves license/provenance review, while integrated deployment acceptance remains pending.

## Consolidated source

The [Nextendo-integrated backend](../servers/n64/README.md) and [owner-tested lab snapshot](../servers/n64/lab/README.md) are now preserved separately. The integration adds scoped discovery/lifecycle regressions but has not been manually accepted with the Nextendo account service. See [integration and migration](n64-integration.md). The successful local report does not erase the earlier Ryujinx crashes or establish four-player/remote-network coverage.
