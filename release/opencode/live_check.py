#!/usr/bin/env python3
"""Check that the pinned OpenCode derives the live model facts of the fixture model record."""
import argparse
import json
from pathlib import Path
import tempfile

from generate import model_facts

CATALOG = Path(__file__).resolve().parent.parent.parent / 'mc' / 'opencodecatalog'
FIXTURES = CATALOG / 'testdata'
# mc shows these facts and gives the model record to OpenCode in the agent container.
FACTS = ('name', 'context', 'output', 'input_price', 'output_price', 'address', 'package', 'variants', 'digest')


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--binary', required=True)
    parser.add_argument('--update', action='store_true', help='Write the facts of the fixture model record.')
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    catalog = {provider['id']: provider for provider in json.loads((CATALOG / 'catalog.json').read_text())['providers']}
    source = json.loads((FIXTURES / 'live-record.json').read_text())
    # mc keeps only the release catalog providers and takes their name, key variable, and website from the release catalog.
    record = {identifier: {'id': identifier, 'name': catalog[identifier]['name'], 'env': [catalog[identifier]['key_variable']],
                           'doc': catalog[identifier]['website'], 'models': source[identifier]['models']}
              for identifier in catalog if identifier in source}
    with tempfile.TemporaryDirectory(prefix='culler-live-check-') as directory:
        path = Path(directory) / 'record.json'
        path.write_text(json.dumps(record))
        facts = {'baseline': [provider for provider in model_facts(binary) if provider['id'] in record],
                 'live': [provider for provider in model_facts(binary, path) if provider['id'] in record]}
        text = json.dumps(facts, indent=1, sort_keys=True) + '\n'
        expected = FIXTURES / 'live-facts.json'
        if args.update:
            expected.write_text(text)
            print('The fixture facts are current. Write the live models again with the mc/opencodecatalog tests.')
            return
        if expected.read_text() != text:
            raise SystemExit('The pinned OpenCode derives other facts from the fixture model record. '
                             'Run live_check.py --update, then write mc/opencodecatalog/testdata/live-models.json again.')
        derived = {(provider['id'], model['id']): model for provider in facts['live'] for model in provider['models']}
        checked = 0
        for provider, models in json.loads((FIXTURES / 'live-models.json').read_text()).items():
            for model in models:
                path.write_text(model['record'])
                single = {(row['id'], fact['id']): fact for row in model_facts(binary, path) for fact in row['models']}
                want, got = derived[(provider, model['id'])], single.get((provider, model['id']))
                if not got or any(got[key] != want[key] for key in FACTS):
                    raise SystemExit(f"The model record of {provider}/{model['id']} gives other facts than the live model list.")
                if any(model[key] != want[key] for key in ('context', 'input_price', 'output_price', 'package', 'variants')):
                    raise SystemExit(f"The live model {provider}/{model['id']} differs from the facts of the pinned OpenCode.")
                checked += 1
        if not checked:
            raise SystemExit('The fixture holds no live model.')
        print(f'The pinned OpenCode derives the facts of {checked} live models from their model records.')


if __name__ == '__main__':
    main()
