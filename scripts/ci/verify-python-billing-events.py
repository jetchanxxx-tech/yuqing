#!/usr/bin/env python3
"""Reject empty/skipped engine billing acceptance on hosted runners."""
import sys
import xml.etree.ElementTree as ET

root = ET.parse(sys.argv[1]).getroot()
cases = root.findall(".//testcase")
required = ("test_usage_survives_invalid_json_and_retry", "test_usage_outbox_recovers_after_ack_loss")
assert cases, "engine suite was empty"
assert all(case.find("skipped") is None and case.find("failure") is None and case.find("error") is None for case in cases), "engine suite contains skipped or failed cases"
for name in required:
    assert any(case.attrib.get("name", "").startswith(name) for case in cases), f"required actual billing behavior did not run: {name}"
print(f"Verified {len(cases)} Python engine sandbox cases; zero skipped/failed cases.")
