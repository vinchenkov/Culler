#!/usr/bin/env python3
"""F0b-2 root algorithm — reference derivation for root_vectors.json.

NOT production code; same discipline as F0b-1/reference_encoder.py: production
verifiers (B07) conform to root_vectors.json. Re-running MUST reproduce the
committed file byte-for-byte. algorithm_id = "shz-merkle-v1".

Spec (docs/design/f0b-2-root-algorithm.md):
  leaf(id, seq, chain_digest) = SHA-256(0x00 ‖ u64be(len(idNFC)) ‖ idNFC ‖ u64be(seq) ‖ chain_digest32)
  order: raw UTF-8 byte-lexicographic ascending on idNFC; duplicate ids are malformed input
  node(l, r)                  = SHA-256(0x01 ‖ l ‖ r)
  build: RFC-6962 style — if a level has odd length > 1, duplicate its last node
  empty: EMPTY_ROOT = SHA-256(UTF8("shz-root-empty-v1"))
"""
import hashlib
import json
import unicodedata
from pathlib import Path

ALGORITHM_ID = "shz-merkle-v1"
EMPTY_ROOT = hashlib.sha256(b"shz-root-empty-v1").hexdigest()


def u64(n):
    return n.to_bytes(8, "big")


def leaf(agg_id, seq, chain_digest):
    if not isinstance(seq, int) or seq < 0:
        raise ValueError(f"seq must be a non-negative integer: {seq!r}")
    raw = unicodedata.normalize("NFC", agg_id).encode("utf-8")
    digest = bytes.fromhex(chain_digest)
    if len(digest) != 32:
        raise ValueError("chain_digest must be 32 bytes")
    return hashlib.sha256(b"\x00" + u64(len(raw)) + raw + u64(seq) + digest).digest()


def merkle(leaves):
    if not leaves:
        return bytes.fromhex(EMPTY_ROOT)
    level = list(leaves)
    while len(level) > 1:
        if len(level) % 2 == 1:
            level.append(level[-1])  # RFC-6962 last-node duplication
        level = [hashlib.sha256(b"\x01" + level[i] + level[i + 1]).digest() for i in range(0, len(level), 2)]
    return level[0]


def root(entries):
    seen = set()
    leaves = []
    for agg_id, seq, chain_digest in sorted(entries, key=lambda e: unicodedata.normalize("NFC", e[0]).encode("utf-8")):
        key = unicodedata.normalize("NFC", agg_id).encode("utf-8")
        if key in seen:
            raise ValueError(f"duplicate aggregate_id in chain-head set: {agg_id!r}")
        seen.add(key)
        leaves.append(leaf(agg_id, seq, chain_digest))
    return merkle(leaves).hex()


A = "aa" * 32
B = "bb" * 32
C = "cc" * 32


def case(cid, semantic, entries):
    try:
        return {"id": cid, "semantic": semantic, "entries": [list(e) for e in entries],
                "expected_root": root(entries), "expect_error": None}
    except ValueError as e:
        return {"id": cid, "semantic": semantic, "entries": [list(e2) for e2 in entries],
                "expected_root": None, "expect_error": str(e)}


CASES = [
    case("R1-empty", "empty set is EMPTY_ROOT (domain-derived, never null)", []),
    case("R2-single", "one leaf: root == that leaf (no re-hash of a lone node)", [("run/1", 1, A)]),
    case("R3-pair", "two leaves in byte order", [("run/1", 1, A), ("run/2", 7, B)]),
    case("R4-odd", "three leaves exercise last-node duplication", [("a", 1, A), ("b", 2, B), ("c", 3, C)]),
    case("R5-four", "balanced tree", [("a", 1, A), ("b", 2, B), ("c", 3, C), ("d", 4, A)]),
    case("R6-order-bytes", "uppercase byte before lowercase (raw UTF-8 order, not case-insensitive)", [("Z", 1, A), ("a", 1, B)]),
    case("R6b-order-stable", "same set shuffled must equal R6", [("a", 1, B), ("Z", 1, A)]),
    case("R7-dup-id", "duplicate aggregate_id is malformed input", [("x", 1, A), ("x", 2, B)]),
    case("R8-bad-digest", "non-32-byte chain_digest is malformed", [("x", 1, "deadbeef")]),
    case("R9-nfc", "decomposed e+U+0301 must equal precomposed U+00E9 at leaf (built via chr to defeat editor normalization)", [("cafe" + chr(0x301), 1, A)]),
    case("R9b-nfc", "precomposed counterpart of R9", [("caf" + chr(0xE9), 1, A)]),
]


def main():
    out = {
        "algorithm_id": ALGORITHM_ID,
        "status": "registered-pending-approval",
        "empty_root": EMPTY_ROOT,
        "rules": {
            "leaf": "SHA256(0x00||u64be(len(idNFC))||idNFC||u64be(seq)||chain_digest32)",
            "node": "SHA256(0x01||left||right)",
            "order": "raw UTF-8 byte-lexicographic ascending on NFC(aggregate_id); duplicates rejected",
            "odd_level": "duplicate the last node (RFC-6962)",
            "empty": "EMPTY_ROOT = SHA256(UTF8('shz-root-empty-v1'))",
            "history": "roots are interpreted under the algorithm_id they were produced with; changing rules requires a new id",
        },
        "cases": CASES,
    }
    data = json.dumps(out, ensure_ascii=False, indent=1, sort_keys=True).encode("utf-8") + b"\n"
    path = Path(__file__).parent / "root_vectors.json"
    old = path.read_bytes() if path.exists() else None
    path.write_bytes(data)
    print(f"cases={len(CASES)} -> root_vectors.json sha256={hashlib.sha256(data).hexdigest()}")
    r6 = next(c for c in CASES if c["id"] == "R6-order-bytes")
    r6b = next(c for c in CASES if c["id"] == "R6b-order-stable")
    assert r6["expected_root"] == r6b["expected_root"], "shuffle must not move the root"
    r9 = next(c for c in CASES if c["id"] == "R9-nfc")
    r9b = next(c for c in CASES if c["id"] == "R9b-nfc")
    assert r9["expected_root"] == r9b["expected_root"], "NFC pair must match"
    assert next(c for c in CASES if c["id"] == "R7-dup-id")["expect_error"], "duplicate must error"
    assert next(c for c in CASES if c["id"] == "R8-bad-digest")["expect_error"], "bad digest must error"
    if old is not None and old != data:
        print("WARNING: root_vectors.json drifted from previous run")


if __name__ == "__main__":
    main()
