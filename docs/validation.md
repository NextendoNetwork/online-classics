# Import validation

On October 5, 2026, each of the Genesis, GBA and SNES modules passed:

- `go test ./... -timeout 60s`
- `go vet ./...`
- `go build`

These checks apply to the source snapshots recorded in provenance.json. They do not establish gameplay or deployment compatibility after integration. Only Go source/tests, module manifests, original license/credit notices and contribution documentation were selected for this import. No binary patches or private runtime files were copied.

## NES and Game Boy update — October 5, 2026

Both exported modules passed `go test ./... -timeout 60s`, `go vet ./...` and `go build` locally. The tests ran against the exported sources after replacing historical LAN fixture addresses. Existing Genesis, GBA and SNES implementation files are unchanged in this update; their checks are repeated by the five-module GitHub workflow.

The publication checker passed the indexed repository file set. The root LICENSE.md has no changes. The NES/Game Boy source manifest records original file hashes and the indexed exported hashes (after Git text normalization); only documentation and synthetic test-fixture addresses differ from the selected lab inputs.

The GitHub workflow additionally runs the content check on pushes and pull requests. Its result is a content gate, not legal clearance. The manually reported gameplay scope is recorded in test-matrix.md; N64 was excluded from the initial NES/Game Boy update.

## N64 consolidation — October 5, 2026

Both N64 modules passed tests, vet and build locally. The account-integrated module retains the upstream tests and adds regression coverage for bounded Classics configuration aliases, exact property precedence and host/guest departure behavior. The lab preserves its existing N64 matchmaking tests after fixture-address sanitation. CI includes both modules independently.

These checks validate source behavior. Citron/Switch success was owner-reported on the lab backend; manual acceptance of the integrated account-service deployment and four-player gameplay remain pending. Original N64 and root PolyForm Shield notices, and the derived lab's MIT attribution, are retained.
