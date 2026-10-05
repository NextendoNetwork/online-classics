# Source architecture

Each server is an independent Go module with a main package. The implementations share a lab origin, but their title-specific configuration and changes need comparison before deduplication.

| Files | Responsibility |
| --- | --- |
| main.go, npln.go | Startup, options, tenant/application defaults, HTTP/2 and RPC dispatch |
| auth.go, session_token.go | Lab identities and signed session token profiles |
| local_friends.go, local_presence.go, local_messaging.go | Explicit identity pairing, presence and messaging |
| match_rooms.go, rooms.go | Session registry, search, joining and lifecycle |
| gamesync_store.go, gamesync_document.go, gamesync_write.go | Room-owned documents and write behavior |
| gamesync_stream.go, gamesync_signaling.go, gamesync_user_state.go | Watches, signaling and user state |
| ice.go, turn_local.go | Connectivity and relay experiments |
| *_test.go | Protocol and behavior regression tests |

The transport manually encodes and decodes protobuf messages. Successful automated checks validate the tested lab behavior; they do not certify complete upstream protocol compatibility.

## Title references

| Title | Application ID | Version | Observed tenant |
| --- | --- | --- | --- |
| Genesis | 0100B3C014BDA000 | 3.1.1 | t-7b4e32ca-lp1 |
| GBA | 010012F017576000 | 3.3.0 | t-7b4e32ca-lp1 |
| SNES | 01008D300C50C000 | 6.0.0 | t-4bdb1dd3-lp1 |

Confirm the current game Build ID and destination when preparing clients. Version metadata does not establish that a separately supplied binary patch applies.

## GBA discovery changes

GBA creates LCLA6-4P sessions and searches LCLA6. Its server accepts the observed LCLA6-2P and LCLA6-4P configurations while retaining user, property, capacity and closed-session filtering. Creation/search use consoleName/ConsoleName and applicationVersion/ApplicationVersion aliases; exact keys take precedence. Tests cover mismatched values and unrelated configurations.

The lab-genesis-query-members experiment is off by default. Enabling it alone did not resolve Switch-host discovery. The recovery test also used client friend-cache and routing changes, so its outcome cannot be attributed solely to this flag.

## SNES and Genesis limitations

SNES retains some Genesis diagnostic names because it derives from that backend. Optional experiments should remain explicitly selected. The historical unimplemented Auth/IssueToken observation and later successful Switch run must be reconciled against actual deployed revisions before inferring production authentication behavior.

Genesis Citron suspension freezes remain unresolved. Successful GBA and SNES tests do not establish a fix for Genesis.
