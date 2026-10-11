#!/usr/bin/env python3
"""Reject empty/skipped engine billing acceptance on hosted runners."""
import sys
import xml.etree.ElementTree as ET

root = ET.parse(sys.argv[1]).getroot()
cases = root.findall(".//testcase")
required = ("test_usage_survives_invalid_json_and_retry", "test_usage_outbox_recovers_after_ack_loss", "test_report_product_never_succeeds_when_usage_fsync_fails", "test_insight_dimension_does_not_retry_fatal_usage_durability_failure", "test_observed_stream_usage_survives_later_parser_or_provider_error", "test_usage_on_provider_error_chunk_is_recorded", "test_configured_engine_health_remains_readable_and_workloads_require_token")
assert cases, "engine suite was empty"
assert all(case.find("skipped") is None and case.find("failure") is None and case.find("error") is None for case in cases), "engine suite contains skipped or failed cases"
for name in required:
    assert any(case.attrib.get("name", "").startswith(name) for case in cases), f"required actual billing behavior did not run: {name}"
print(f"Verified {len(cases)} Python engine sandbox cases; zero skipped/failed cases.")
