# Shared account verifier

The verifier is adapted from this contributor's original Go Nextendo verifier in `NextendoNetwork/ultimate-chicken-horse-server`, revision `ef38293`, `internal/backend/nextendo.go`. That original Go server contribution is MIT licensed. This copy retains MIT licensing; see LICENSE.

It verifies an operator-trusted signed client credential, the enclosed Nextendo account proof and the required account-service online gate. It contains no Unity DLLs, game code or console material. Its account authority, public keys and signing scope must be provisioned by the operator; passing its synthetic tests does not establish compatibility with every client release.
