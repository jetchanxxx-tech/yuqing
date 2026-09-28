#!/bin/bash
# Read-only beta release gate: no migrations, compilation, or service changes.
set -euo pipefail

fail() { printf 'BLOCKED: %s\n' "$*" >&2; exit 1; }

if [ "${1:-}" != '--beta-0.2.3' ] || [ "$#" -ne 1 ]; then
  fail 'Specify --beta-0.2.3; CLI migrate --list/--status are unsupported and cannot validate schema.'
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
for name in server worker cli; do
  [ -s "$REPO_ROOT/platform/bin/yuqing-$name" ] && [ -x "$REPO_ROOT/platform/bin/yuqing-$name" ] ||
    fail "Missing prebuilt executable: platform/bin/yuqing-$name (build off-host)."
done
[ -s "$REPO_ROOT/web/dist/index.html" ] || fail 'Missing prebuilt web/dist/index.html (build off-host).'

# Evidence paths are operator-supplied; their existence does not prove live acceptance.
for variable in YUQING_BACKUP_EVIDENCE YUQING_RESTORE_EVIDENCE YUQING_MIGRATION_TEST_EVIDENCE YUQING_LIVE_SCHEMA_EVIDENCE YUQING_SYSTEMD_EVIDENCE YUQING_ROLLBACK_EVIDENCE YUQING_QUEUE_EVIDENCE YUQING_NOTIFICATION_EVIDENCE; do
  path="${!variable:-}"
  [ -n "$path" ] && [ -f "$path" ] && [ -s "$path" ] || fail "Missing $variable: provide a nonempty, reviewed evidence file."
done

# Current runtime is process-local; refuse even if evidence files are supplied.
if grep -q 'q := queue.NewMemory()' "$REPO_ROOT/platform/internal/app/container.go" ||
   grep -q 'falling back to memory' "$REPO_ROOT/platform/cmd/worker/main.go"; then
  fail 'Persistent queue unavailable: restart/replay and multi-replica acceptance required.'
fi
if grep -q 'runtime:notifications.*未接通' "$REPO_ROOT/platform/internal/business/monitorplan/service.go"; then
  fail 'Notification delivery unavailable: verify test-inbox delivery and audit records.'
fi

printf '%s\n' 'Local beta 0.2.3 artifacts and evidence present; NOT deployment or live acceptance.'
