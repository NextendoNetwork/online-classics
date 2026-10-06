# Maintainer integration

1. Review the source provenance and per-directory licensing.
2. Run tests, vet and build in each independent server module.
3. Compare title-specific tenant/application defaults, matchmaking filters and Gamesync behavior before sharing handlers.
4. Replace lab identity pairing with trusted account verification and authorization.
5. Configure persistent signing keys, certificates, destinations and relay access using the deployment's secret management.
6. Define room expiration, reconnect behavior, tenant isolation, limits and operational logging.
7. Reproduce two-client local tests with the exact client versions and separately provided, authorized patches.
8. Validate physical Switch clients and then separate networks in both host directions.

Client work belongs in separate changes: Prelude installation/routing, Ryujinx friend-cache behavior and Citron diagnostics should not be presented as server fixes. No production deployment or SpeedRunners 2 support is established by this contribution.

Submit subsequent contributions through a pull request against main. Include the automated check results and the limitations above. Merge reviewed changes using the repository's permitted workflow and keep private lab archives out of Git.
