#!/usr/bin/env python3
"""Load the pinned web search plugin without a provider request."""
import json
from pathlib import Path
import sys
import tempfile

from generate import environment, run

with tempfile.TemporaryDirectory(prefix='culler-plugin-check-') as directory:
    root = Path(directory)
    packages = Path(sys.argv[2]).resolve()
    env = environment(root)
    env['OPENCODE_CONFIG_CONTENT'] = json.dumps({
        'plugins': [(packages / 'node_modules/opencode-websearch').as_uri(), (packages / 'control').as_uri()],
        'websearch': {'provider': 'native'},
    })
    result = run(str(Path(sys.argv[1]).resolve()), root, env, ['api', '--standalone', 'GET', '/api/model'])
    if result.returncode or b'plugin.error' in result.stderr or b'PluginLoadError' in result.stderr:
        raise SystemExit('The pinned OpenCode plugins failed to load.')
    print('The pinned OpenCode plugins loaded.')
