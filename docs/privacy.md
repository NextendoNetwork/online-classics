# Network address privacy

Do not publish the operator's PC, console, home-network or public residential IP addresses. Deployment endpoints belong in private operator configuration. Examples must use placeholders or documentation addresses; tests requiring private-address classification use synthetic `10.77.0.0/16` fixtures. These fixtures are not deployment destinations.

The publication checker rejects personal-network-style address literals in the indexed source. Keep account data, captures, logs and runtime configuration outside Git. Review pull request descriptions and commit messages as well as files before publication.

Current-file sanitation does not remove earlier commits, forks, caches or existing clones. Historical removal requires a separately coordinated history rewrite and remediation of affected published references.
