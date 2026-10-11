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

## Approved administration planning scope (2026-10-09)

The administrator backend is organized into user account management and tenant/subscription management. The development director's plan is `docs/planning/ADMIN_USER_CENTER_DEVELOPMENT_PLAN.html`.

Login device management is deferred for this iteration: device lists, device fingerprints, per-device sign-out, and device session pages are outside scope. Account security requirements for password changes and user/tenant suspension remain part of the plan and may use account status or credential versions without introducing device management.

The user authorized sequential implementation on 2026-10-09. Complete each task with GitHub-hosted acceptance evidence and record progress in docs/dev/ADMIN_USER_CENTER_DELIVERY_STATUS.html. Do not report proposed or incomplete features as implemented or deployed.

## Confirmed business rules (2026-10-09)

Account closure has a seven-day withdrawal window. Unsettled orders block completion. Closing a sole-member team requires explicit confirmation; other members' assets must remain intact. Financial ledgers remain but personal data is anonymized, and business files follow existing plan retention.

Notification integration uses sandbox tests for this implementation. Do not send real email or SMS without separately supplied, authorized test recipients. Real supplier delivery remains unverified until separately tested.

Only the fixed administrator account initially identified by admin@pangu.com is exempt from usage-count and spending restrictions. Bind that exception to its verified immutable user ID, not an untrusted token email, a platform-admin role, or an entire tenant. All other accounts follow their selected plan's charging and limits; the previous global PostgreSQL beta bypass must be removed after atomic billing is implemented. Record actual usage and provider cost even for the exempt account.
