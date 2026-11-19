#!/usr/bin/env python3
"""F0b-1 canonical serializer — REFERENCE ENCODER & vector generator.

NOT production code. This script derives the cross-implementation test vectors
committed as vectors.json next to it. Production serializers (B04+/C03) are
conformant implementations written against vectors.json, per DC §3.4 ⑧ and
AM §4b. Re-running this file MUST reproduce vectors.json byte-for-byte.

Encoding contract (registered in docs/design/f0b-serializer.md, serializer_id
"shz-canon-v1"). Deviations from DC §3.4 ①-⑧ semantics are bugs in THIS file.
"""
import hashlib
import json
import re
import sys
from pathlib import Path

SERIALIZER_ID = "shz-canon-v1"

ABSENT, NULL, VALUE = 0, 1, 2

DEC_RE = re.compile(r"^-?(0|[1-9][0-9]*)(\.[0-9]+)?$")
TIME_RE = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{6}Z$")
HEX32_RE = re.compile(r"^[0-9a-f]{64}$")


class NotConfirmed(Exception):
    pass


def u64(n):
    return n.to_bytes(8, "big")


def enc_str(s):
    b = s.encode("utf-8")
    return u64(len(b)) + b


def enc_scalar(kind, v):
    if kind == "dec":
        if not isinstance(v, str) or not DEC_RE.match(v):
            raise NotConfirmed(f"decimal lexical form rejected: {v!r}")
        return enc_str(v)
    if kind == "dec_int":
        if not isinstance(v, str) or not re.match(r"^(0|[1-9][0-9]*)$", v):
            raise NotConfirmed(f"decimal-integer form rejected: {v!r}")
        return enc_str(v)
    if kind == "time":
        if not isinstance(v, str) or not TIME_RE.match(v):
            raise NotConfirmed(f"time must be canonical RFC3339 UTC microsecond 'Z' form: {v!r}")
        return enc_str(v)
    if kind == "hex32":
        if not isinstance(v, str) or not HEX32_RE.match(v):
            raise NotConfirmed(f"hex32 must be 64 lowercase hex chars: {v!r}")
        return enc_str(v)
    if kind.startswith("enum:"):
        allowed = kind[5:].split("|")
        if v not in allowed:
            raise NotConfirmed(f"enum {allowed} rejects {v!r}")
        return enc_str(v)
    if kind == "str":
        if not isinstance(v, str):
            raise NotConfirmed("string required")
        # ④: NFC normalization is part of the canonical form.
        return enc_str(unicodedata_nfc(v))
    raise NotConfirmed(f"unknown scalar kind {kind}")


def unicodedata_nfc(s):
    import unicodedata
    return unicodedata.normalize("NFC", s)


def enc_field(out, kind, present, value, name):
    if present == ABSENT:
        out.append(bytes([ABSENT]))
        return
    if present == NULL:
        out.append(bytes([NULL]))
        return
    out.append(bytes([VALUE]))
    if kind.startswith("obj:"):
        enc_object(out, SCHEMAS[kind[4:]], value, name)
    elif kind.startswith("arr:"):
        inner = kind[4:]
        if not isinstance(value, list):
            raise NotConfirmed(f"array required for {name}")
        out.append(u64(len(value)))
        for item in value:
            # array elements may be NULL or VALUE, never ABSENT
            if item is None:
                out.append(bytes([NULL]))
            else:
                out.append(bytes([VALUE]))
                out.append(enc_scalar(inner, item))
    else:
        out.append(enc_scalar(kind, value))


def enc_object(out, table, obj, ctx):
    keys = set(obj.keys())
    declared = {f[0] for f in table}
    unknown = keys - declared
    if unknown:
        # ⑥: unknown fields are an error, never skipped
        raise NotConfirmed(f"unknown field(s) {sorted(unknown)} in {ctx}")
    for fname, fkind, fopt in table:
        if fname not in obj:
            if fopt:
                enc_field(out, fkind, ABSENT, None, fname)
            else:
                raise NotConfirmed(f"required field {fname} missing in {ctx}")
            continue
        v = obj[fname]
        if v is None:
            if not fopt:
                raise NotConfirmed(f"required field {fname} cannot be explicit NULL in {ctx}")
            enc_field(out, fkind, NULL, None, fname)
        else:
            enc_field(out, fkind, VALUE, v, fname)


# ENVELOPE table third element = OPTIONAL flag (True = may be absent/null)
ENVELOPE = [
    ("serializer_id", "enum:" + SERIALIZER_ID + "|shz-canon-v0", False),
    ("event_id", "hex32", False),
    ("aggregate_id", "str", False),
    ("aggregate_type", "str", False),
    ("seq", "dec_int", False),
    ("event_type", "str", False),
    ("occurred_at", "time", False),
    ("recorded_at", "time", False),
    ("effective_at", "time", True),
    ("supersedes_event_id", "hex32", True),
    ("causation_id", "str", True),
    ("correlation_id", "str", True),
    ("prev_hash", "hex32", False),
    ("payload", "str_marker", False),  # replaced at encode time by obj:<family>
]


def encode_event(ev):
    """ev: dict with envelope keys plus "payload_family" and "payload"."""
    family = ev["payload_family"]
    out = []
    declared = {f[0] for f in ENVELOPE} | {"payload_family"}
    unknown = set(ev.keys()) - declared
    if unknown:
        raise NotConfirmed(f"unknown envelope field(s) {sorted(unknown)}")
    for fname, fkind, fopt in ENVELOPE:
        if fname == "payload":
            fkind = "obj:" + family
        if fname not in ev:
            if fopt:
                enc_field(out, fkind, ABSENT, None, fname)
            else:
                raise NotConfirmed(f"required envelope field {fname} missing")
            continue
        v = ev[fname]
        if v is None:
            if not fopt:
                raise NotConfirmed(f"required envelope field {fname} cannot be explicit NULL")
            enc_field(out, fkind, NULL, None, fname)
        else:
            enc_field(out, fkind, VALUE, v, fname)
    return b"".join(out)


# ---- payload tables (declared order is normative; ⑤; third element = OPTIONAL) ----

SCHEMAS = {
    # usage observation, PS §4/§4.1 — declared order frozen (NOT alphabetical;
    # V4c pins this order in canonical bytes so sorting implementations fail).
    "usage_observation": [
        ("model_id", "str", True),
        ("output_tokens", "dec", True),
        ("input_tokens", "dec", True),
        ("provider_request_id", "str", False),
        ("local_request_id", "str", True),
        ("origin", "enum:provider|local", False),
        ("event_seq", "dec_int", False),
        ("source_version", "dec_int", False),
        ("report_kind", "enum:cumulative|incremental", False),
        ("finality", "enum:interim|final", False),
        ("identity_provability", "enum:native|cursor|none", False),
        ("usage_family", "str", False),
    ],
    # usage settlement, BL §4 — source identity quadruple + digest + supersedes chain
    "usage_settle": [
        ("source_report_id", "str", False),
        ("authorization_id", "str", False),
        ("provider_request_id", "str", False),
        ("source_version", "dec_int", False),
        ("payload_digest", "hex32", False),
        ("supersedes_report_id", "str", True),
        ("effect", "enum:reserved|confirmed|released|expired|partial_settled|reconciliation|written_off", False),
    ],
    # admission, DC §3.3 — dedup key = grant_id × event_type
    "admission": [
        ("grant_id", "str", False),
        ("reason", "str", True),
        ("expires_at", "time", True),
    ],
    # probe schema pins V4c order independence from JSON key order
    "probe_order": [
        ("zeta", "str", False),
        ("alpha", "str", False),
    ],
}

GENESIS_SALT = "shz-genesis-v1:"


def genesis(aggregate_type):
    return hashlib.sha256((GENESIS_SALT + unicodedata_nfc(aggregate_type)).encode("utf-8")).hexdigest()


def chain_digest(prev_hex, event_digest_hex):
    return hashlib.sha256(bytes.fromhex(prev_hex) + bytes.fromhex(event_digest_hex)).hexdigest()


def base_event(**over):
    ev = {
        "serializer_id": SERIALIZER_ID,
        "event_id": "11" * 32,
        "aggregate_id": "agg-1",
        "aggregate_type": "run",
        "seq": "1",
        "event_type": "probe",
        "occurred_at": "2026-11-19T12:00:00.000000Z",
        "recorded_at": "2026-11-19T12:00:00.000001Z",
        "prev_hash": genesis("run"),
        "payload_family": "probe_order",
        "payload": {"zeta": "z", "alpha": "a"},
    }
    ev.update(over)
    return ev


def usage_event(**over):
    ev = {
        "serializer_id": SERIALIZER_ID,
        "event_id": "22" * 32,
        "aggregate_id": "usage-1",
        "aggregate_type": "usage",
        "seq": "1",
        "event_type": "observation",
        "occurred_at": "2026-11-19T12:00:00.000000Z",
        "recorded_at": "2026-11-19T12:00:00.000002Z",
        "prev_hash": genesis("usage"),
        "payload_family": "usage_observation",
        "payload": {
            "model_id": "probe-model",
            "output_tokens": "87",
            "input_tokens": "150",
            "provider_request_id": "prov-req-1",
            "origin": "provider",
            "event_seq": "1",
            "source_version": "1",
            "report_kind": "cumulative",
            "finality": "final",
            "identity_provability": "native",
            "usage_family": "openai-chat",
        },
    }
    payload_over = over.pop("payload", {})
    ev.update(over)
    ev["payload"] = {**ev["payload"], **payload_over}
    return ev


def case(cid, cls, semantic, ev=None, expect=None, note=""):
    try:
        data = encode_event(ev)
        digest = hashlib.sha256(data).hexdigest()
        return {
            "id": cid, "class": cls, "semantic": semantic, "input": ev,
            "expected_canonical_hex": data.hex(), "expected_event_digest": digest,
            "expect_error": None, "note": note,
        }
    except NotConfirmed as e:
        return {
            "id": cid, "class": cls, "semantic": semantic, "input": ev,
            "expected_canonical_hex": None, "expected_event_digest": None,
            "expect_error": str(e), "note": note,
        }


def build():
    vectors = []

    # ---- AM §4b class ①: numeric lexical survival (1 vs 1.0) ----
    vectors.append(case("V1a", "numeric-lexical", "input_tokens as '1'",
                        usage_event(payload={"input_tokens": "1"})))
    vectors.append(case("V1b", "numeric-lexical", "input_tokens as '1.0' — DIFFERENT digest from V1a",
                        usage_event(payload={"input_tokens": "1.0"})))
    vectors.append(case("V1c", "numeric-lexical", "input_tokens as '1.00' — DIFFERENT from V1a and V1b",
                        usage_event(payload={"input_tokens": "1.00"})))

    # ---- class ②: absent ≠ null ----
    vectors.append(case("V2a", "absent-vs-null", "supersedes_event_id omitted (ABSENT)",
                        usage_event()))
    vectors.append(case("V2b", "absent-vs-null", "supersedes_event_id explicit null (NULL) — DIFFERENT digest from V2a",
                        usage_event(supersedes_event_id=None)))

    # ---- class ③: NFC ----
    vectors.append(case("V3a", "nfc-normalization", "model_id precomposed U+00E9 (built via chr to defeat editor normalization)",
                        usage_event(payload={"model_id": "caf" + chr(0xE9)})))
    vectors.append(case("V3b", "nfc-normalization", "model_id decomposed e+U+0301 — SAME digest as V3a (NFC applied)",
                        usage_event(payload={"model_id": "cafe" + chr(0x301)})))

    # ---- class ④: declaration order, not JSON order, not alphabetical ----
    vectors.append(case("V4a", "declaration-order", "payload keys {zeta,alpha}",
                        base_event()))
    vectors.append(case("V4b", "declaration-order", "same event, keys written {alpha,zeta} — SAME digest as V4a",
                        base_event(payload={"alpha": "a", "zeta": "z"})))
    vectors.append(case("V4c", "declaration-order",
                        "canonical bytes follow SCHEMA order zeta-before-alpha; alphabetical implementations diverge",
                        base_event(payload={"zeta": "z", "alpha": "a"}),
                        note="expected_canonical_hex pins zeta's bytes before alpha's; see schema usage_observation: output_tokens precedes input_tokens likewise"))

    # ---- class ⑤: serializer_id immutability / historical recomputation ----
    vectors.append(case("V5a", "serializer-version", "event under shz-canon-v1",
                        usage_event()))
    vectors.append(case("V5b", "serializer-version", "same logical event under shz-canon-v0 (legacy) — DIFFERENT digest; historical events MUST be recomputed under their recorded id (⑦)",
                        usage_event(serializer_id="shz-canon-v0")))

    # ---- ⑥: unknown field is an error ----
    ev_bad = usage_event()
    ev_bad["surprise"] = 1
    vectors.append(case("V6a", "unknown-field", "unknown envelope field 'surprise' — MUST error NOT_CONFIRMED", ev_bad))
    ev_bad2 = usage_event(payload={"whatever": 1})
    vectors.append(case("V6b", "unknown-field", "unknown payload field 'whatever' — MUST error NOT_CONFIRMED", ev_bad2))

    # ---- ②/③ grammar guards (lexical strictness) ----
    for i, (cid, val) in enumerate([("V7a", "+1"), ("V7b", "01"), ("V7c", "1e3"), ("V7d", "1.")]):
        vectors.append(case(cid, "grammar-guard", f"decimal {val!r} rejected at serializer (write path must store canonical lexical)",
                            usage_event(payload={"input_tokens": val})))
    vectors.append(case("V7e", "grammar-guard", "time without microsecond padding rejected (canonical fixed-width)",
                        usage_event(recorded_at="2026-11-19T12:00:00Z")))

    # ---- PS §4 family extension vectors (usage_observation schema) ----
    fam = lambda name, **kw: usage_event(event_id=hashlib.sha256(name.encode()).hexdigest(), **kw)

    vectors.append(case("F-openai-chat-final", "family",
                        "openai-chat final chunk = unique last cumulative snapshot (PS §4)",
                        fam("openai-chat-final")))
    vectors.append(case("F-anthropic-final", "family",
                        "anthropic combined semantics: input fixed at message_start, output = last delta, converged to ONE cumulative final snapshot",
                        fam("anthropic-final", payload={"usage_family": "anthropic", "input_tokens": "1200", "output_tokens": "350"})))
    vectors.append(case("F-anthropic-interim", "family",
                        "anthropic event-level interim: output_tokens ABSENT at mid-stream (interim cumulative, supersedes nothing yet)",
                        fam("anthropic-interim", payload={"usage_family": "anthropic", "output_tokens": None, "finality": "interim"}),
                        note="output_tokens None here means explicit NULL case; the absent case omits the key — V2a/V2b already pin the byte difference"))
    vectors.append(case("F-anthropic-interim-absent", "family",
                        "anthropic interim with output_tokens ABSENT (key omitted) — differs from the explicit-NULL interim above",
                        {"serializer_id": SERIALIZER_ID, "event_id": hashlib.sha256(b"anthropic-interim-absent").hexdigest(),
                         "aggregate_id": "usage-1", "aggregate_type": "usage", "seq": "1", "event_type": "observation",
                         "occurred_at": "2026-11-19T12:00:00.000000Z", "recorded_at": "2026-11-19T12:00:00.000002Z",
                         "prev_hash": genesis("usage"), "payload_family": "usage_observation",
                         "payload": {"model_id": "a-probe", "input_tokens": "1200",
                                     "provider_request_id": "a-req-1", "origin": "provider", "event_seq": "1",
                                     "source_version": "1", "report_kind": "cumulative", "finality": "interim",
                                     "identity_provability": "native", "usage_family": "anthropic"}}))
    vectors.append(case("F-google-partial", "family",
                        "google usageMetadata partial: output_tokens ABSENT = partial fact, NOT zero (PS §4)",
                        {"serializer_id": SERIALIZER_ID, "event_id": hashlib.sha256(b"google-partial").hexdigest(),
                         "aggregate_id": "usage-1", "aggregate_type": "usage", "seq": "1", "event_type": "observation",
                         "occurred_at": "2026-11-19T12:00:00.000000Z", "recorded_at": "2026-11-19T12:00:00.000002Z",
                         "prev_hash": genesis("usage"), "payload_family": "usage_observation",
                         "payload": {"model_id": "g-probe", "input_tokens": "77",
                                     "provider_request_id": "g-req-1", "origin": "provider", "event_seq": "1",
                                     "source_version": "1", "report_kind": "cumulative", "finality": "interim",
                                     "identity_provability": "native", "usage_family": "google"}}))
    vectors.append(case("F-responses-supersedes", "family",
                        "openai-responses completed duplicated/late → monotonic version supersedes: v2 event carries supersedes_event_id of v1",
                        {"serializer_id": SERIALIZER_ID, "event_id": hashlib.sha256(b"responses-v2").hexdigest(),
                         "aggregate_id": "usage-1", "aggregate_type": "usage", "seq": "2", "event_type": "observation",
                         "occurred_at": "2026-11-19T12:00:01.000000Z", "recorded_at": "2026-11-19T12:00:01.000003Z",
                         "prev_hash": genesis("usage"),
                         "supersedes_event_id": hashlib.sha256(b"responses-v1").hexdigest(),
                         "payload_family": "usage_observation",
                         "payload": {"model_id": "probe-model", "output_tokens": "90", "input_tokens": "150",
                                     "provider_request_id": "prov-req-1", "origin": "provider", "event_seq": "2",
                                     "source_version": "2", "report_kind": "cumulative", "finality": "final",
                                     "identity_provability": "native", "usage_family": "openai-responses"}}))
    vectors.append(case("F-unknown-family", "family",
                        "unknown family: no usage numbers enter confirmed — values ABSENT, interim unprovable (D12: no guessing/estimating)",
                        {"serializer_id": SERIALIZER_ID, "event_id": hashlib.sha256(b"unknown-family").hexdigest(),
                         "aggregate_id": "usage-1", "aggregate_type": "usage", "seq": "3", "event_type": "observation",
                         "occurred_at": "2026-11-19T12:00:02.000000Z", "recorded_at": "2026-11-19T12:00:02.000002Z",
                         "prev_hash": genesis("usage"), "payload_family": "usage_observation",
                         "payload": {"provider_request_id": "u-req-1", "origin": "provider", "event_seq": "1",
                                     "source_version": "1", "report_kind": "incremental", "finality": "interim",
                                     "identity_provability": "none", "usage_family": "unknown"}}))
    vectors.append(case("F-local-origin", "family",
                        "unverifiable identity: provider_request_id='unverifiable', local_request_id carries identity, origin=local, provability=none → UNKNOWN/RECONCILIATION channel only (PS §4.1④ red line)",
                        fam("local-origin", payload={"provider_request_id": "unverifiable", "local_request_id": "loc-req-9",
                                                     "origin": "local", "identity_provability": "none"})))

    # ---- settle + admission registered shapes ----
    vectors.append(case("S-settle-confirmed", "settle",
                        "BL §4 settle identity: quadruple + payload_digest + supersedes chain; effect=confirmed",
                        {"serializer_id": SERIALIZER_ID, "event_id": hashlib.sha256(b"settle-1").hexdigest(),
                         "aggregate_id": "budget-1", "aggregate_type": "budget", "seq": "1", "event_type": "settle",
                         "occurred_at": "2026-11-19T12:00:03.000000Z", "recorded_at": "2026-11-19T12:00:03.000002Z",
                         "prev_hash": genesis("budget"), "payload_family": "usage_settle",
                         "payload": {"source_report_id": "rep-1", "authorization_id": "auth-1",
                                     "provider_request_id": "prov-req-1", "source_version": "1",
                                     "payload_digest": "ab" * 32, "effect": "confirmed"}}))
    vectors.append(case("A-admission-expiry", "admission",
                        "DC §3.3 expiry: effective_at=business expiry instant (= grant's expires_at), recorded_at=observation time; two columns never conflated; dedup key grant_id×event_type",
                        {"serializer_id": SERIALIZER_ID, "event_id": hashlib.sha256(b"admission-1").hexdigest(),
                         "aggregate_id": "admission-1", "aggregate_type": "admission", "seq": "1", "event_type": "expiry",
                         "occurred_at": "2026-11-19T12:00:04.000000Z", "recorded_at": "2026-11-19T12:00:04.000009Z",
                         "effective_at": "2026-11-19T12:00:04.000000Z",
                         "prev_hash": genesis("admission"), "payload_family": "admission",
                         "payload": {"grant_id": "grant-7", "reason": "ttl"}}))
    vectors.append(case("G-genesis-run", "genesis",
                        "GENESIS(aggregate_type)=SHA256('shz-genesis-v1:'+NFC(type)); different types → different genesis (domain separation, anti chain-swap)",
                        {"serializer_id": SERIALIZER_ID, "event_id": "33" * 32, "aggregate_id": "x-1",
                         "aggregate_type": "task", "seq": "1", "event_type": "transition",
                         "occurred_at": "2026-11-19T12:00:05.000000Z", "recorded_at": "2026-11-19T12:00:05.000002Z",
                         "prev_hash": genesis("task"), "payload_family": "probe_order", "payload": {"zeta": "z", "alpha": "a"}}))
    return vectors


def main():
    vectors = build()
    # cross-vector relation assertions (classes ①②③④⑤ expected relations)
    by = {v["id"]: v for v in vectors}
    assert by["V1a"]["expected_event_digest"] != by["V1b"]["expected_event_digest"] != by["V1c"]["expected_event_digest"]
    assert by["V2a"]["expected_event_digest"] != by["V2b"]["expected_event_digest"]
    assert by["V3a"]["expected_event_digest"] == by["V3b"]["expected_event_digest"]
    assert json.dumps(by["V3a"]["input"]) != json.dumps(by["V3b"]["input"]), "V3 inputs must be raw-different"
    assert by["F-anthropic-interim"]["expected_event_digest"] != by["F-anthropic-interim-absent"]["expected_event_digest"]
    assert by["V4a"]["expected_event_digest"] == by["V4b"]["expected_event_digest"]
    assert by["V5a"]["expected_event_digest"] != by["V5b"]["expected_event_digest"]
    assert by["V6a"]["expect_error"] and by["V6b"]["expect_error"]
    assert all(v["expect_error"] for k, v in by.items() if k.startswith("V7"))
    out = {
        "serializer_id": SERIALIZER_ID,
        "status": "registered-pending-F0b-3-batch-approval",
        "hash_construction": "event_digest=SHA256(canonical_bytes); chain_digest_i=SHA256(chain_digest_{i-1}_bytes||event_digest_i_bytes); genesis=SHA256('shz-genesis-v1:'+NFC(aggregate_type))",
        "vectors": vectors,
    }
    path = Path(__file__).parent / "vectors.json"
    data = json.dumps(out, ensure_ascii=False, indent=1, sort_keys=True).encode("utf-8")
    old = path.read_bytes() if path.exists() else None
    path.write_bytes(data + b"\n")
    print(f"vectors: {len(vectors)} cases -> {path.name} sha256={hashlib.sha256(data + bytes(1)).hexdigest()}")
    if old is not None and old != data + b"\n":
        print("WARNING: vectors.json changed from previous run — determinism break in derivation")


if __name__ == "__main__":
    sys.exit(main())
