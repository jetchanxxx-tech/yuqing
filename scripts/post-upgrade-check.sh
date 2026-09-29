#!/usr/bin/env bash
set -euo pipefail

app_root="${YUQING_APP_ROOT:-/opt/yuqing}"
database="${YUQING_PLATFORM_DB:-yuqing_platform}"
api_base="${YUQING_API_BASE:-http://127.0.0.1:8080}"
expected_version="${YUQING_EXPECTED_GOOSE_VERSION:-12}"
health_file="$(mktemp)"
trap 'rm -f -- "$health_file"' EXIT

systemctl is-active yuqing-server yuqing-worker yuqing-query yuqing-media yuqing-insight yuqing-report yuqing-forum
curl --fail --silent --show-error --retry 10 --retry-connrefused --retry-delay 2 --max-time 5 "$api_base/api/v1/health" > "$health_file"
python3 - "$health_file" <<'PY'
import json, sys
with open(sys.argv[1]) as handle:
    health = json.load(handle)
if health.get('status') != 'ok' or health.get('version') != '0.2.3-beta':
    raise SystemExit('Unexpected API health/version')
print('API health/version verified')
PY
for port in 8000 8001 8002 8003 8004; do
  curl --fail --silent --show-error --retry 10 --retry-connrefused --retry-delay 2 --max-time 5 "http://127.0.0.1:$port/health"
  printf '\n'
done
actual_version="$(runuser -u postgres -- psql -XAt -d "$database" -c 'SELECT max(version_id) FROM goose_db_version WHERE is_applied')"
test "$actual_version" = "$expected_version"
runuser -u postgres -- psql -XAt -d "$database" -c "SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename IN ('queue_messages','monitor_plans') ORDER BY tablename"
sha256sum "$app_root/bin/yuqing-server" "$app_root/bin/yuqing-worker" "$app_root/bin/yuqing-cli" "$app_root/web/dist/index.html"
printf 'Runtime/schema health verified; authenticated provider E2E and full feature acceptance remain separate.\n'
