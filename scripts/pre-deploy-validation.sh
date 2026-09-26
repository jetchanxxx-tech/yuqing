#!/bin/bash
# Pre-deployment validation: Verify database schema matches code expectations
set -euo pipefail

echo "=== Pre-Deployment Schema Validation ==="
echo ""

# Configuration
YUQING_CONFIG="${YUQING_CONFIG:-/opt/yuqing/config/config.yaml}"
CLI_BIN="${CLI_BIN:-bin/yuqing-cli}"

if [ ! -f "$CLI_BIN" ]; then
    echo "ERROR: CLI binary not found at $CLI_BIN"
    exit 1
fi

if [ ! -f "$YUQING_CONFIG" ]; then
    echo "ERROR: Config file not found at $YUQING_CONFIG"
    exit 1
fi

# Get expected migrations from CLI binary
echo "[1/3] Checking CLI binary migrations..."
EXPECTED_MIGRATIONS=$($CLI_BIN migrate --list 2>/dev/null | tail -1 | awk '{print $NF}')
if [ -z "$EXPECTED_MIGRATIONS" ]; then
    echo "ERROR: Could not determine expected migrations from CLI"
    exit 1
fi
echo "Expected migrations in binary: 0001-$EXPECTED_MIGRATIONS"

# Get applied migrations from database
echo "[2/3] Checking database applied migrations..."
APPLIED_COUNT=$(YUQING_CONFIG="$YUQING_CONFIG" $CLI_BIN migrate --status 2>/dev/null | grep -c "applied" || true)
if [ -z "$APPLIED_COUNT" ]; then
    echo "ERROR: Could not query database migration status"
    exit 1
fi
echo "Applied migrations in database: $APPLIED_COUNT"

# Compare
echo "[3/3] Validating schema compatibility..."
EXPECTED_COUNT=$(echo "$EXPECTED_MIGRATIONS" | sed 's/^0*//')
if [ "$APPLIED_COUNT" -lt "$EXPECTED_COUNT" ]; then
    echo ""
    echo "❌ VALIDATION FAILED"
    echo "Database schema is behind code expectations"
    echo "  Expected: $EXPECTED_COUNT migrations"
    echo "  Applied:  $APPLIED_COUNT migrations"
    echo ""
    echo "Action required:"
    echo "  cd /opt/yuqing"
    echo "  YUQING_CONFIG=$YUQING_CONFIG $CLI_BIN migrate platform"
    echo ""
    exit 1
fi

echo ""
echo "✅ VALIDATION PASSED"
echo "Schema is compatible with code (migrations: $APPLIED_COUNT/$EXPECTED_COUNT)"
exit 0
