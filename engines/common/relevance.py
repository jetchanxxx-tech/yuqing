"""Conservative, versioned admission for untrusted web search excerpts.

This does not infer synonyms, brands, comments, or full-text coverage.
"""
import re
import unicodedata
from dataclasses import dataclass

ADMISSION_VERSION = "lexical-v1"
_ALNUM = re.compile(r"^[a-z0-9]+$", re.IGNORECASE)
_SPA_BOILERPLATE = re.compile(
    r"(?:we[’']?re\s+sorry.*(?:react\s+app|javascript\s+enabled)|"
    r"(?:react\s+app|vue\s+app).*doesn[’']?t\s+work.*javascript|"
    r"please\s+enable\s+javascript)", re.IGNORECASE | re.DOTALL,
)


@dataclass(frozen=True)
class Admission:
    accepted: bool
    reason: str


def _normalize(value: str) -> str:
    return " ".join(unicodedata.normalize("NFKC", value or "").casefold().split())


def usable_excerpt(excerpt: str) -> bool:
    normalized = _normalize(excerpt)
    return bool(normalized) and not _SPA_BOILERPLATE.search(normalized)


def _part_matches(part: str, text: str) -> bool:
    # ASCII identifiers (especially short model names) must be whole tokens.
    if _ALNUM.fullmatch(part):
        return bool(re.search(r"(?<![a-z0-9])" + re.escape(part) + r"(?![a-z0-9])", text))
    compact = re.sub(r"\s+", "", text)
    part = re.sub(r"\s+", "", part)
    left = r"(?<![a-z0-9])" if part[0].isascii() and part[0].isalnum() else ""
    right = r"(?![a-z0-9])" if part[-1].isascii() and part[-1].isalnum() else ""
    return bool(re.search(left + re.escape(part) + right, compact))


def admit(keyword: str, title: str, excerpt: str) -> Admission:
    if not usable_excerpt(excerpt):
        return Admission(False, "unusable_excerpt")
    parts = _normalize(keyword).split()
    if not parts:
        return Admission(False, "irrelevant")
    text = _normalize(title + " " + excerpt)
    return Admission(True, "keyword_match") if all(_part_matches(part, text) for part in parts) else Admission(False, "irrelevant")
