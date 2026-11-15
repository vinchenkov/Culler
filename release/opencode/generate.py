#!/usr/bin/env python3
"""Extract the catalog and record requests from the pinned OpenCode program."""
import argparse
import http.server
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import threading
from urllib.parse import urlparse

ROOT = Path(__file__).resolve().parent
PINS = json.loads((ROOT / 'pins.json').read_text())
# mc embeds the same plugin for the model facts of the live model list.
FACTS = ROOT.parent.parent / 'mc' / 'opencodecatalog' / 'facts'
KEY = 'culler-catalog-probe'
# The adapter sets the key variable in the environment of the agent, and the commands of the agent inherit it.
# The catalog omits a provider whose key variable holds the credential of another tool.
TOOL_VARIABLES = {'GITHUB_TOKEN'}


def environment(root):
    env = {key: os.environ[key] for key in ('PATH', 'TMPDIR') if key in os.environ}
    env.update(HOME=str(root), OPENCODE_DISABLE_MODELS_FETCH='1', OPENCODE_DISABLE_FREE_MODELS='1',
               OPENCODE_DISABLE_AUTOUPDATE='1', OPENCODE_DISABLE_PROJECT_CONFIG='1',
               OPENCODE_DISABLE_CLAUDE_CODE='1', OPENCODE_DISABLE_FILEWATCHER='1')
    for name in ('DATA', 'CONFIG', 'CACHE', 'STATE'):
        env[f'XDG_{name}_HOME'] = str(root / name.lower())
    return env


def run(binary, root, env, args):
    return subprocess.run([binary, *args], cwd=root, env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=60)


def model_facts(binary, record=None):
    """Return the facts that the program derives from its own model record or from a model record file."""
    with tempfile.TemporaryDirectory(prefix='culler-model-facts-') as directory:
        root = Path(directory)
        env = environment(root)
        if record:
            env['OPENCODE_MODELS_PATH'] = str(Path(record).resolve())
        env['CULLER_MODEL_FACTS'] = str(root / 'facts.json')
        env['OPENCODE_CONFIG_CONTENT'] = json.dumps({'plugins': [FACTS.as_uri()]})
        result = run(binary, root, env, ['api', '--standalone', 'GET', '/api/model'])
        if result.returncode:
            raise RuntimeError(result.stderr.decode())
        return json.loads((root / 'facts.json').read_text())


def fixed_address(value):
    address = urlparse(value)
    return address.scheme == 'https' and address.hostname and not (address.username or address.password or
           address.query or address.fragment or re.search(r'[{}$<>]', value))


def capture(binary, entry, model):
    recorded = []

    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_POST(self):
            body = json.loads(self.rfile.read(int(self.headers['content-length'])))
            recorded.append({'path': self.path, 'headers': dict(self.headers), 'model': body.get('model')})
            self.send_response(401)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(b'{"error":{"message":"Catalog probe","type":"authentication_error"}}')

    server = http.server.HTTPServer(('127.0.0.1', 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        with tempfile.TemporaryDirectory(prefix='culler-catalog-request-') as directory:
            root = Path(directory)
            env = environment(root)
            env[entry['key_variable']] = KEY
            address = f'http://127.0.0.1:{server.server_port}/probe'
            env['OPENCODE_CONFIG_CONTENT'] = json.dumps({
                'model': f"{entry['id']}/{model['id']}",
                'providers': {entry['id']: {'settings': {'baseURL': address, 'apiKey': KEY},
                    'models': {model['id']: {'settings': {'baseURL': address}}}}},
                'agents': {'title': {'disabled': True}, 'summary': {'disabled': True}},
            })
            result = run(binary, root, env, ['run', '--standalone', '--format', 'json', '--title', 'Catalog probe', 'Say OK.'])
            if not recorded:
                raise RuntimeError(f"No request for {model['package']}: {result.stderr.decode()[-600:]} {result.stdout.decode()[-600:]}")
    finally:
        server.shutdown()
        server.server_close()
    request = recorded[0]
    placements = [(name.lower(), value) for name, value in request['headers'].items() if KEY in value]
    if len(placements) != 1:
        raise RuntimeError(f"Unexpected key placement: {model['package']}")
    header, value = placements[0]
    placement = 'bearer' if header == 'authorization' and value == f'Bearer {KEY}' else header
    if request['model'] == model['id']:
        location = 'body'
    elif model['id'] in request['path']:
        location = 'path'
    else:
        raise RuntimeError(f"Unexpected model location: {model['package']}")
    return {'key_placement': placement, 'model_location': location,
            'request': {'path': request['path'], 'key_header': header, 'model': request['model']}}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True)
    parser.add_argument('--plugin', required=True, help='The unpacked web search plugin folder.')
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    with tempfile.TemporaryDirectory(prefix='culler-catalog-') as directory:
        root = Path(directory)
        env = environment(root)
        version = run(binary, root, env, ['--version']).stdout.decode().strip()
        if PINS['opencode'] not in version:
            raise RuntimeError('The OpenCode version differs from the release pin.')
    providers = model_facts(binary)
    plugin = Path(args.plugin)
    if json.loads((plugin / 'package.json').read_text())['version'] != PINS['websearch']:
        raise RuntimeError('The web search plugin version differs from the release pin.')
    source = (plugin / 'dist/index.js').read_text()
    registry = re.search(r'var PROVIDER_TYPES_BY_ID = (\{.*?\n\});', source, re.S)
    if not registry:
        raise RuntimeError('The web search registry changed.')
    search_ids = set(re.findall(r'^  ["\']?([\w-]+)["\']?:', registry.group(1), re.M))
    entries = []
    packages = {}
    for provider in sorted(providers, key=lambda provider: provider['id']):
        if len(provider['key_variables']) != 1 or provider['key_variables'][0] in TOOL_VARIABLES:
            continue
        models = [m for m in provider['models'] if m['type'] == 'chat' and m['status'] != 'deprecated' and m['tools']]
        if not models:
            continue
        if any(not fixed_address(m['address']) or not m['package'].startswith('@opencode/ai/providers/') for m in models):
            continue
        entries.append({'id': provider['id'], 'name': provider['name'], 'website': provider['website'],
                        'key_variable': provider['key_variables'][0], 'web_search': provider['id'] in search_ids,
                        'models': [{key: m[key] for key in ('id', 'name', 'context', 'input_price', 'output_price',
                                                            'address', 'package', 'variants')} for m in models]})
    for entry in entries:
        for model in entry['models']:
            package = model['package']
            if package not in packages:
                print(f'Record the request for {package}.', flush=True)
                packages[package] = capture(binary, entry, model)
            model.update({k: v for k, v in packages[package].items() if k != 'request'})
    entries = [entry for entry in entries if all(model['key_placement'] in
               ('bearer', 'x-api-key', 'x-goog-api-key') for model in entry['models'])]
    result = {'version': 1, 'opencode_version': PINS['opencode'], 'websearch_version': PINS['websearch'],
              'providers': entries, 'requests': packages}
    Path(args.output).write_text(json.dumps(result, indent=2, sort_keys=True) + '\n')
    print(f'The catalog contains {len(entries)} providers.', flush=True)


if __name__ == '__main__':
    main()
