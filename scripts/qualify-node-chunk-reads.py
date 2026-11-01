#!/usr/bin/env python3
"""Explicit test-only consumer override; never a production dependency update."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

NODE = '86a254841276ede899b22379a9cdea4f9632465a'
BASE = '29e6d9b30be94de3772c850c7a1e9811585a8833'
ROOT_TEST = 'TestCoreChunkReadFullClientRecovery'
HELPER = 'orderbook/full_client_recovery_export_test.go'
FIXTURES = ['core_chunk_read_export_test.go', 'core_chunk_read_recovery_test.go']
BEFORE = b'\tstarted = true\n\tfmt.Println("owned_full_client_started " + fullClientDefaultsProfile)'
AFTER = b'\tstarted = true\n\tstartCoreChunkReadObserver(t, f, directory)\n\tfmt.Println("owned_full_client_started " + fullClientDefaultsProfile)'
REPLACE = b'replace github.com/Zenoglyphs-VM/zvm-core => ./zvm-core'


def require(ok, message):
    if not ok:
        raise ValueError(message)


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def git(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args], timeout=20)


def changed_helper(raw):
    require(raw.count(BEFORE) == 1 and AFTER not in raw, 'Original helper insertion is not unique')
    return raw.replace(BEFORE, AFTER, 1)


def module_override(raw, root):
    require(raw.count(REPLACE) == 1, 'Original module override differs')
    encoded = json.dumps(str(root.resolve())).encode('ascii')
    return raw.replace(REPLACE, b'replace github.com/Zenoglyphs-VM/zvm-core => ' + encoded, 1)


def check_red(raw, actual_exit):
    require(actual_exit == 1 and len(raw) <= 512 << 10, 'Baseline is not the bounded actual native failure')
    events = [json.loads(line) for line in raw.splitlines() if line.strip()]
    package = 'github.com/Zenoglyphs-VM/zvm-node/orderbook'
    require(events and all(isinstance(e, dict) and e.get('Package') == package for e in events), 'Unexpected baseline package')
    failed = [e.get('Test') for e in events if e.get('Action') == 'fail' and e.get('Test')]
    require(failed == [ROOT_TEST + '/invalid-author', ROOT_TEST], 'Baseline failure is outside the declared consumer assertion')
    require(sum(e.get('Action') == 'fail' and not e.get('Test') for e in events) == 1, 'Missing native package failure')
    require(not any(e.get('Action') == 'skip' or e.get('Action') == 'pass' and e.get('Test') in failed for e in events), 'Baseline was skipped or contradicted')
    messages = [e for e in events if e.get('Action') == 'output' and 'core_chunk_read_advanced ' in e.get('Output', '')]
    require(len(messages) == 1 and messages[0].get('Test') == ROOT_TEST + '/invalid-author', 'Missing unique actual advancement assertion')
    require(re.search(r'core_chunk_read_advanced source=8 engine=7 derived=(7|8) invalid_assembled=(0|1)\s*$', messages[0]['Output']) is not None, 'Unexpected live engine/durable horizon')
    text = ''.join(e.get('Output', '') for e in events)
    require('panic:' not in text and 'DATA RACE' not in text and 'test timed out' not in text and 'build failed' not in text, 'Unrelated baseline failure')
    return {'actual_exit': actual_exit, 'baseline_expected_failure': True, 'failed_tests': failed,
            'actual_engine_head': 7, 'blocked_source_momentum': 8, 'source_dependency_adopted': False}


def self_test():
    raw = BEFORE + b'\n'
    require(changed_helper(raw).replace(AFTER, BEFORE, 1) == raw, 'Helper inverse failed')
    package = 'github.com/Zenoglyphs-VM/zvm-node/orderbook'
    events = [dict(Package=package, Action='output', Test=ROOT_TEST+'/invalid-author',
                   Output='fixture_test.go:99: core_chunk_read_advanced source=8 engine=7 derived=8 invalid_assembled=1\n'),
              dict(Package=package, Action='fail', Test=ROOT_TEST+'/invalid-author'),
              dict(Package=package, Action='fail', Test=ROOT_TEST), dict(Package=package, Action='fail')]
    encode = lambda rows: ('\n'.join(json.dumps(row) for row in rows)+'\n').encode()
    check_red(encode(events), 1)
    cases = [(encode(events), 0), (encode(events[:-1]), 1), (encode(events+events[:1]), 1)]
    for old, new in [('engine=7', 'engine=6'), ('derived=8', 'derived=9'), ('source=8', 'source=9'),
                     ('invalid_assembled=1', 'invalid_assembled=2'), (ROOT_TEST+'/invalid-author', ROOT_TEST+'/missing-table')]:
        cases.append((encode(events).replace(old.encode(), new.encode()), 1))
    for tail in ['panic: unrelated', 'DATA RACE', 'test timed out', 'build failed']:
        cases.append((encode(events+[dict(Package=package, Action='output', Output=tail)]), 1))
    for bad, code in cases:
        try: check_red(bad, code)
        except (ValueError, json.JSONDecodeError): pass
        else: raise AssertionError('Baseline reader accepted a refusal control')
    for bad in [b'', BEFORE+BEFORE, AFTER]:
        try: changed_helper(bad)
        except ValueError: pass
        else: raise AssertionError('Helper accepted a drifted source')
    require(module_override(REPLACE+b'\n', Path('/tmp/declared-core')).count(b'/tmp/declared-core') == 1, 'Module override control failed')
    print(json.dumps({'positive_controls': 4, 'refusal_controls': len(cases)+3, 'all_controls_passed': True}))


def prepare(core, node, baseline, output):
    core, node, baseline, output = [p.resolve(strict=True) for p in [core, node, baseline, output]]
    require(len({core, node, baseline, output}) == 4, 'Consumer roots must be independent')
    require(git(node, 'rev-parse', 'HEAD').decode().strip() == NODE, 'Node source changed')
    require(git(baseline, 'rev-parse', 'HEAD').decode().strip() == BASE, 'Core baseline changed')
    gitlink = git(node, 'ls-tree', NODE, 'zvm-core').decode().strip()
    require(gitlink == '160000 commit '+BASE+'\tzvm-core', 'Actual production Core gitlink differs')
    require(not git(node, 'status', '--porcelain', '--untracked-files=all'), 'Node fixture checkout was already dirty')
    require(not git(core, 'status', '--porcelain', '--untracked-files=all'), 'Core candidate checkout was already dirty')
    source = node/HELPER
    original = git(node, 'show', NODE+':'+HELPER)
    require(source.read_bytes() == original, 'Original helper changed before insertion')
    source.write_bytes(changed_helper(original))
    pins = {HELPER: digest(source.read_bytes())}
    for name in FIXTURES:
        target = node/'orderbook'/name
        require(not target.exists(), 'Consumer fixture already exists')
        raw = (core/'scripts/core-consumer-fixtures'/name).read_bytes()
        with target.open('xb') as handle: handle.write(raw)
        pins['orderbook/'+name] = digest(raw)
    original_module = git(node, 'show', NODE+':go.mod')
    original_sum = git(node, 'show', NODE+':go.sum')
    for label, root in [('baseline', baseline), ('candidate', core)]:
        with (output/(label+'.mod')).open('xb') as handle: handle.write(module_override(original_module, root))
        with (output/(label+'.sum')).open('xb') as handle: handle.write(original_sum)
    manifest = dict(schema=1, node=NODE, baseline=BASE, core=git(core,'rev-parse','HEAD').decode().strip(),
                    core_tree=git(core,'rev-parse','HEAD^{tree}').decode().strip(), helper_original_sha256=digest(original),
                    source_pins=pins, test_only_override=True, production_gitlink_modified=False,
                    full_C02_N04_acceptance=False)
    with (output/'manifest.json').open('x') as handle: json.dump(manifest, handle, sort_keys=True, indent=2); handle.write('\n')
    verify(core, node, baseline, output)
    print(json.dumps(manifest, sort_keys=True))


def verify(core, node, baseline, output):
    core, node, baseline, output = [p.resolve(strict=True) for p in [core, node, baseline, output]]
    manifest = json.loads((output/'manifest.json').read_bytes())
    require(git(node,'rev-parse','HEAD').decode().strip() == manifest['node'] == NODE, 'Node source changed')
    require(git(baseline,'rev-parse','HEAD').decode().strip() == manifest['baseline'] == BASE, 'Baseline source changed')
    require(git(core,'rev-parse','HEAD').decode().strip() == manifest['core'], 'Candidate source changed')
    require(git(core,'rev-parse','HEAD^{tree}').decode().strip() == manifest['core_tree'], 'Candidate tree changed')
    require(not git(core, 'status', '--porcelain', '--untracked-files=all'), 'Core candidate changed')
    require(not git(baseline, 'status', '--porcelain', '--untracked-files=all'), 'Core baseline changed')
    require(git(node,'diff','--name-only').decode().splitlines() == [HELPER], 'Undeclared tracked consumer modification')
    require(git(node,'ls-files','--others','--exclude-standard').decode().splitlines() == sorted('orderbook/'+name for name in FIXTURES), 'Undeclared new consumer files')
    original = git(node, 'show', NODE+':'+HELPER)
    require((node/HELPER).read_bytes() == changed_helper(original), 'Consumer helper delta differs')
    require(digest(original) == manifest['helper_original_sha256'], 'Original helper proof differs')
    for name in FIXTURES:
        require((node/'orderbook'/name).read_bytes() == (core/'scripts/core-consumer-fixtures'/name).read_bytes(), 'Copied consumer test differs')
    for name, value in manifest['source_pins'].items(): require(digest((node/name).read_bytes()) == value, 'Consumer source digest differs')
    original_module = git(node, 'show', NODE+':go.mod')
    original_sum = git(node, 'show', NODE+':go.sum')
    require((node/'go.mod').read_bytes() == original_module and (node/'go.sum').read_bytes() == original_sum, 'Production modules changed')
    for label, root in [('baseline', baseline), ('candidate', core)]:
        require((output/(label+'.mod')).read_bytes() == module_override(original_module, root), 'Test module override differs')
        require((output/(label+'.sum')).read_bytes() == original_sum, 'Pinned test module sums changed')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=['self-test','prepare','verify','check-red'])
    parser.add_argument('--core', type=Path)
    parser.add_argument('--node', type=Path)
    parser.add_argument('--baseline', type=Path)
    parser.add_argument('--output', type=Path)
    parser.add_argument('--log', type=Path)
    parser.add_argument('--actual-exit', type=int)
    args = parser.parse_args()
    if args.mode == 'self-test': self_test()
    elif args.mode == 'check-red':
        require(args.log is not None and args.log.stat().st_size <= 512 << 10, 'Oversized/missing baseline log')
        print(json.dumps(check_red(args.log.read_bytes(), args.actual_exit), sort_keys=True))
    else:
        require(all(value is not None for value in [args.core,args.node,args.baseline,args.output]), 'Missing explicit consumer root')
        function = prepare if args.mode == 'prepare' else verify
        function(args.core,args.node,args.baseline,args.output)


if __name__ == '__main__':
    main()
