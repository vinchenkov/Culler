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
KEY = 'culler-catalog-probe'


def environment(root):
    env = {key: os.environ[key] for key in ('PATH', 'TMPDIR') if key in os.environ}
    env.update(HOME=str(root), OPENCODE_DISABLE_MODELS_FETCH='1', OPENCODE_DISABLE_FREE_MODELS='1',
               OPENCODE_DISABLE_AUTOUPDATE='1', OPENCODE_DISABLE_PROJECT_CONFIG='1',
               OPENCODE_DISABLE_CLAUDE_CODE='1', OPENCODE_DISABLE_FILEWATCHER='1')
    for name in ('DATA', 'CONFIG', 'CACHE', 'STATE'):
        env[f'XDG_{name}_HOME'] = str(root / name.lower())
    return env


def run(binary, root, env, args):
    return subprocess.run([binary, *args], cwd=root, env=env, capture_output=True, timeout=60)


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
        env['CULLER_CATALOG_OUTPUT'] = str(root)
        env['OPENCODE_CONFIG_CONTENT'] = json.dumps({'plugins': [ROOT.as_uri()]})
        result = run(binary, root, env, ['api', '--standalone', 'GET', '/api/model'])
        if result.returncode:
            raise RuntimeError(result.stderr.decode())
        snapshot = json.loads((root / 'snapshot.json').read_text())
        providers = json.loads((root / 'providers.json').read_text())
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
    for row in sorted(providers, key=lambda row: row['provider']['id']):
        provider = row['provider']
        raw = snapshot.get(provider['id'])
        if not raw or len(raw.get('env', [])) != 1:
            continue
        models = [m for m in row['models'] if m.get('type', 'chat') == 'chat' and
                  m.get('status') != 'deprecated' and m.get('capabilities', {}).get('tools')]
        if not models:
            continue
        entry = {'id': provider['id'], 'name': provider['name'], 'website': raw.get('doc', ''),
                 'key_variable': raw['env'][0], 'web_search': provider['id'] in search_ids, 'models': []}
        invalid = False
        for model in models:
            address = model.get('settings', {}).get('baseURL', provider.get('settings', {}).get('baseURL', ''))
            package = model.get('package', provider['package'])
            if not fixed_address(address) or not package.startswith('@opencode/ai/providers/'):
                invalid = True
                break
            price = model.get('cost', [{}])[0]
            item = {'id': model['id'], 'name': model['name'], 'context': model['limit']['context'],
                    'input_price': price.get('input', 0), 'output_price': price.get('output', 0),
                    'address': address, 'package': package, 'variants': [v['id'] for v in model.get('variants', [])]}
            entry['models'].append(item)
        if invalid:
            continue
        entries.append(entry)
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
