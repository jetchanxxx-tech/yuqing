# Project execution policy

## Build and test location

All compilation, bundling, and compiler based checks must run on GitHub hosted runners through `.github/workflows/build-release.yml` and `.github/workflows/postgresql-tests.yml`. This is the user's instruction of 2026-10-09.

Do not run `go build`, `go test`, `go vet`, `go run`, `make build`, `make test`, `npm run build`, `npm run dev`, `tsc`, or Vite builds/dependency prebundling on local machines, development hosts, deployment servers, or self hosted runners. Dependency installation or package preparation that compiles code must also run on GitHub hosted runners.

Local work is limited to editing, review, lightweight syntax checks, Git operations, downloading artifacts, checksum verification, and uploading verified artifacts. Deployment hosts may verify checksums, unpack artifacts, change configuration, run prebuilt migration tools, restart services, and perform health checks.

A successful GitHub run for the exact source commit is required before release. An incomplete or failed run is not a passing check. Report a failed CI result and fix it through a new commit; do not fall back to local compilation.

## Branches and release artifacts

- `dev`: development branch; pushes and pull requests run cloud checks.
- `prod`: production source baseline; changes arrive through pull requests from `dev` after required checks pass.
- Existing `main` and `beta/*` branches remain available for history and ongoing work.

For production, select the successful run on `prod` for the intended commit. Download `yuqing-release-<SHA>` and verify the archive's `.sha256`, its `BUILD_COMMIT`, and the unpacked `SHA256SUMS` before installation. Preserve rollback artifacts and install hash named frontend assets before atomically replacing `index.html`.

Never commit deployment credentials or tokens.
