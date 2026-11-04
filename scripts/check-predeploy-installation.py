"""One native positive cohort and thirteen separately bounded refusal inputs."""
import argparse, copy, hashlib, json, pathlib, subprocess, sys, tempfile

def digest(raw):
    return hashlib.sha256(raw).hexdigest()

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--input', type=pathlib.Path, required=True)
    parser.add_argument('--binary', type=pathlib.Path, required=True)
    args = parser.parse_args()
    with args.input.open('rb') as stream:
        raw = stream.read((4 << 20) + 1)
    assert 0 < len(raw) <= 4 << 20

    def invoke(path, pin):
        row = subprocess.run([str(args.binary), str(path), pin], stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
        assert len(row.stdout) <= 64 << 10 and len(row.stderr) <= 64 << 10
        sys.stdout.buffer.write(row.stdout)
        sys.stdout.buffer.write(row.stderr)
        sys.stdout.flush()
        return row

    positive = invoke(args.input, digest(raw))
    assert positive.returncode == 0
    rows = [json.loads(line.split(b' ', 1)[1]) for line in positive.stdout.splitlines() if line.startswith(b'CONSTRUCTOR-STATE-NATIVE ')]
    assert [r['profile'] for r in rows] == ['supported-isthmus', 'l2', 'l2-69', 'l2-dp', 'historical-chain-10']
    for index, row in enumerate(rows):
        assert row['canonicalFactoryFrames'] == 2 and row['actualInternalSenderCreatorCreates'] == 1 and row['readOnlyGetterChecks'] == 5
        assert row['completeStateMatchesProductionGenesis'] and row['allTwelveAccountsCommitted'] and row['fullRuntimeBytesCompared']
        assert len(row['completeStateRoots']) == 2 and row['completeStateRoots'][-1] == row['candidateStateRoot']
        assert row['factoryNonce'] == 3 and row['entryPointNonce'] == 2 and row['senderCreatorNonce'] == 1
        assert row['entryPointStatusSlot'] == '2' and row['entryPointStatusValue'] == '1' and row['callerNonceBalanceAndGetterRootsUnchanged']
        assert row['independentGoReferenceMatched'] == (index in (0, 4))
        assert not row['historicalDeploymentTransactionsOrAcceptedRelease']
    assert b'CONSTRUCTOR-STATE-COMPLETE profiles=5 canonicalFactoryFrames=10' in positive.stdout
    original = json.loads(raw)
    cases = [('legacy_root', 'legacy state root differs'), ('before_root', 'constructor pre-state differs'),
             ('step_root', 'constructor post-state differs'), ('candidate_root', 'selected genesis state differs'),
             ('salt', 'canonical salt differs'), ('creation', 'complete creation digest differs'),
             ('runtime', 'constructed runtime differs'), ('child', 'constructed child runtime differs'),
             ('getter', 'constructor getter output differs'), ('nonce', 'legacy account nonce differs'),
             ('unknown_field', 'unknown field'), ('input_digest', 'input digest differs'), ('recipe', 'selected recipe differs')]
    with tempfile.TemporaryDirectory(prefix='constructor-state-negative-', dir=args.input.parent) as directory:
        for name, reason in cases:
            obj = copy.deepcopy(original)
            profile = obj['profiles'][0]
            bad_hash = '0x' + '11' * 32
            if name == 'legacy_root': profile['legacyStateRoot'] = bad_hash
            elif name == 'before_root': profile['beforeRoot'] = bad_hash
            elif name == 'step_root': profile['steps'][0]['afterRoot'] = bad_hash
            elif name == 'candidate_root': profile['candidateStateRoot'] = bad_hash
            elif name == 'salt': profile['steps'][0]['salt'] = bad_hash
            elif name in ('creation', 'runtime'):
                value = profile['steps'][0][name]
                profile['steps'][0][name] = value[:2] + ('00' if value[2:4] != '00' else '01') + value[4:]
            elif name == 'child':
                value = profile['steps'][1]['childRuntime']
                profile['steps'][1]['childRuntime'] = value[:2] + ('00' if value[2:4] != '00' else '01') + value[4:]
            elif name == 'getter': profile['reads'][0]['output'] = bad_hash
            elif name == 'nonce': profile['genesisAccounts'][0]['nonce'] = 2
            elif name == 'unknown_field': obj['unreviewed'] = True
            elif name == 'recipe': profile['predeployState'] = 'source-constructors-v2'
            altered = json.dumps(obj, separators=(',', ':')).encode() if name != 'input_digest' else raw
            path = pathlib.Path(directory) / (name + '.json')
            path.write_bytes(altered)
            pin = '00' * 32 if name == 'input_digest' else digest(altered)
            row = invoke(path, pin)
            assert row.returncode == 101 and reason.encode() in row.stderr and not row.stdout
            print('CONSTRUCTOR-STATE-NEGATIVE ' + json.dumps({'case': name, 'expectedReason': reason, 'actualExit': row.returncode,
                  'inputSha256': digest(altered), 'expectedInputSha256': pin, 'complete': True,
                  'stdoutSha256': digest(row.stdout), 'stderrSha256': digest(row.stderr), 'refusedAtIntendedAssertion': True}, sort_keys=True), flush=True)
    print('CONSTRUCTOR-STATE-NEGATIVE-COMPLETE cases=13 complete=true nativePositiveCohortExecutions=1', flush=True)

if __name__ == '__main__':
    main()
