# Import validation

On October 5, 2026, each of the Genesis, GBA and SNES modules passed:

- `go test ./... -timeout 60s`
- `go vet ./...`
- `go build`

These checks apply to the source snapshots recorded in provenance.json. They do not establish gameplay or deployment compatibility after integration. Only Go source/tests, module manifests, original license/credit notices and contribution documentation were selected for this import. No binary patches or private runtime files were copied.

## NES and Game Boy update — October 5, 2026

Both exported modules passed `go test ./... -timeout 60s`, `go vet ./...` and `go build` locally. The tests ran against the exported sources after replacing historical LAN fixture addresses. Existing Genesis, GBA and SNES implementation files are unchanged in this update; their checks are repeated by the five-module GitHub workflow.

The publication checker passed the indexed repository file set. The root LICENSE.md has no changes. The NES/Game Boy source manifest records original file hashes and the indexed exported hashes (after Git text normalization); only documentation and synthetic test-fixture addresses differ from the selected lab inputs.

The GitHub workflow additionally runs the content check on pushes and pull requests. Its result is a content gate, not legal clearance. The manually reported gameplay scope is recorded in test-matrix.md; N64 is excluded.
