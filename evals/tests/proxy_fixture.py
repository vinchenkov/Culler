"""Synthetic Culler access for tests that replace Docker execution."""


def proxy_credentials(harness="codex"):
    family = "claude-sdk" if harness == "claude" else "codex"
    return {"provider_access": {"version": 1, "harness": family, "authMode": "account",
            "url": "https://host.docker.internal:12345", "token": "culler_proxy_" + "x" * 43,
            "ca": "-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----"}}
