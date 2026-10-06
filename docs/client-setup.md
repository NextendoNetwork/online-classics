# Client setup and acceptance

The Go server alone does not install client support. Confirm the application version and Build ID, matching authorized patch, destination routing and account prerequisites before testing. Do not assume a patch for one title or version applies to another.

## Emulator testing

Prepare separate profiles for two distinct test accounts. Configure each client to reach the selected backend and keep canonical account-service routing consistent with the test configuration. Obtain matching patches separately from their maintainer. Begin with two Ryujinx clients; then test Citron/Ryujinx in both host directions.

The GBA discovery recovery involved a bounded Ryujinx friend-cache experiment and corrected routing. Review those changes independently before attributing recovery to the server. Genesis suspension freezing in Citron remains unresolved.

NES 9.1.0 and Game Boy 4.1.0 acceptance also used separately maintained Ryujinx friend-resolution changes. Use the matching Nextendo client revision; these Go modules do not install that client support. A Game Boy 4.1.0 setup must not be applied to a base 1.0.0 installation.

## Physical Switch testing

Use a separately prepared Prelude/Atmosphere setup that routes the intended service to the test destination. Preserve existing host-blocking rules and confirm whether the active environment uses default or emuMMC host configuration. Install only a patch matching the title Build ID. The original lab used consoles and a PC on the same LAN.

Remote testing requires a deployed, reachable backend and the relevant connectivity services. These snapshots do not establish the address, credentials or deployment state of Nextendo's beta service; maintainers must supply those details before distributing remote instructions.

## Acceptance sequence

1. Verify application version, Build ID, patch and backend/client revisions.
2. Authenticate two distinct users.
3. Create, discover and join a room.
4. Start a multiplayer title and verify both inputs.
5. Record the selected game and test duration.
6. Suspend, resume, return to the catalog and select another game.
7. Leave, recreate and rediscover the room.
8. Reverse host/guest roles and repeat.

Report every stage separately and retain raw logs privately. Extend the matrix only with observed results.
