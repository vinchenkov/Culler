import hashlib
import json
from pathlib import Path

import pytest

from culler_evals.browser import BROWSER_TOOLS, PacketBrowser


SOURCE = Path(__file__).resolve().parents[2]


def test_judge_tool_surface_excludes_source_and_script_access():
    assert {tool["name"] for tool in BROWSER_TOOLS} == {"inspect", "scroll", "follow_link", "click"}
    assert all(tool["inputSchema"].get("additionalProperties") is False for tool in BROWSER_TOOLS)


def test_real_packet_wrapper_visibility_and_interactions(tmp_path):
    source = tmp_path / "source"
    wrapper_hashes = {}
    for name in ("dashboard/src/ui/review.js", "dashboard/src/ui/format.js"):
        path = source / name
        path.parent.mkdir(parents=True, exist_ok=True)
        data = (SOURCE / name).read_bytes()
        path.write_bytes(data)
        wrapper_hashes[name] = hashlib.sha256(data).hexdigest()
    packet = tmp_path / "packet.html"
    packet.write_text('''<!doctype html><html><head><style>
      .hidden{display:none} .transparent{opacity:0} .spacer{height:1600px}
      </style></head><body><h1>Visible summary<span class="transparent">Heading secret</span></h1>
      <a href="#glossary">Glossary<span class="transparent">Link secret</span></a>
      <p class="hidden">Hidden reference secret</p><p class="transparent">Transparent reference secret</p>
      <details><summary>Additional evidence</summary><p>Collapsed reference secret</p></details>
      <script>document.body.innerHTML="A script ran"</script>
      <div class="spacer"></div><h2 id="glossary">Glossary</h2><p>Visible definition.</p>
      <img src="https://example.invalid/network.png"></body></html>''')
    with PacketBrowser(source, packet, tmp_path / "browser") as browser:
        first = browser.call("inspect", {})
        text = json.loads(first["content"][1]["text"])["text"]
        assert "Visible summary" in text
        assert "secret" not in text
        assert "secret" not in first["content"][1]["text"]
        assert "A script ran" not in text
        assert "Visible definition" not in text
        result = browser.call("follow_link", {"href": "#glossary"})
        text = json.loads(result["content"][1]["text"])["text"]
        assert "Visible definition" in text
        with pytest.raises(ValueError, match="outside"):
            browser.call("evaluate", {"script": "document.body.innerHTML"})
        (source / "dashboard/src/ui/review.js").write_text("A later source edit differs from the served wrapper.")
        report = browser.report()
        assert report["wrapper_hashes"] == wrapper_hashes
        assert report["viewport"]["width"] == 1280
        assert report["browser_version"]
        assert len(report["screenshots"]) >= 3
        assert "Glossary" in report["visible_headings"]
        assert report["word_counts"]["expandable_difference"] == 3
        assert report["render"]["failed_images"][0]["section"] == "Glossary"
    records = [json.loads(line) for line in (tmp_path / "browser/interactions.jsonl").read_text().splitlines()]
    assert [record["action"] for record in records] == ["initial", "inspect", "follow_link"]
