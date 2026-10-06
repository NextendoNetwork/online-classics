// Command n64-classics-lab runs the experimental N64 Classics NPLN/Gamesync backend.
//
// The supported client reference is N64 Classics 4.2.0. Requests use gRPC over
// HTTP/2 with manually encoded protobuf messages. The lab implements local
// identity pairing, matchmaking, per-room Gamesync storage and signaling,
// presence, and local STUN/TURN experiments.
//
// Start the review in main.go for service registration and command-line options,
// auth.go for lab identities, match_rooms.go for room creation and joining,
// gamesync_store.go for document ownership and atomic writes, and
// gamesync_stream.go for watches and channel lifecycle. Protocol observations
// and unconfirmed hypotheses are documented beside their implementations.
//
// Integrating with the Nextendo service requires its account validation, TLS
// configuration, external ICE destinations and relay authorization. The current
// executable is a local test backend; Internet gameplay remains unverified.
// See ../../../docs/n64-integration.md for the maintainer handoff and test sequence.
package main
