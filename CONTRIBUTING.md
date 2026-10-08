# Contributing

Keep source comments, documentation, issues and pull requests in English.

Choose the affected server under servers/ and run its tests, vet and build before submitting changes. Explain the observed failure, the resulting behavior, and the exact application/client versions used. Preserve title-specific filters and isolate optional experiments.

For gameplay results, record host/guest roles, topology, backend revision, enabled flags and the outcome of discovery, join, gameplay, suspension, reconnect and host reversal. Mark untested combinations explicitly.

Submit changes through a pull request. Include only redistributable source and documentation. Preserve authorship and license notices. Keep game data, keys, credentials, binary patches, profiles and raw captures out of commits; share sanitized structural evidence where needed.

Run `go run scripts/check-publication.go` from the repository root after staging the intended files. CI repeats this check. Follow docs/publication-policy.md; passing the file check is not a legal clearance or proof that a source contribution has permission to be distributed.
