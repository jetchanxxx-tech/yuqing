"""Offline supplier fixtures for the model-only numbered-source protocol."""
import json


def source_prompt(messages):
    for message in messages:
        if message.get("role") == "user":
            value = json.loads(message["content"])
            if "materials" in value or "original_keywords" in value:
                return value
    raise AssertionError("fixture received no source or planning prompt")


def source_text(row):
    return "".join(unit["text"] for unit in row["source_units"])


def first_unit_ids(row):
    return [row["source_units"][0]["id"]]
