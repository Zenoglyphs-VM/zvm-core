"""Execute one positive cohort and six distinct bounded negative inputs."""
import argparse,copy,hashlib,json,pathlib,subprocess,sys,tempfile
def digest(raw):return hashlib.sha256(raw).hexdigest()
def main():
    parser=argparse.ArgumentParser();parser.add_argument('--input',type=pathlib.Path,required=True);parser.add_argument('--binary',type=pathlib.Path,required=True)
    args=parser.parse_args();raw=args.input.read_bytes();assert 0<len(raw)<=4<<20
    def invoke(path,pin):
        row=subprocess.run([str(args.binary),str(path),pin],stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=30)
        assert len(row.stdout)<=64<<10 and len(row.stderr)<=64<<10
        sys.stdout.buffer.write(row.stdout);sys.stdout.buffer.write(row.stderr);sys.stdout.flush()
        return row
    positive=invoke(args.input,digest(raw));assert positive.returncode==0
    rows=[json.loads(line.split(b' ',1)[1]) for line in positive.stdout.splitlines() if line.startswith(b'SYSTEM-PREDEPLOY-NATIVE ')]
    assert [r['profile'] for r in rows]==['supported-isthmus','l2','l2-69','l2-dp']
    for r in rows:
        assert r['contexts']==6 and r['actualProtocolSystemHandlerCalls']==12 and r['actualNonsystemGetterChecks']==60 and len(r['completeStateRoots'])==12
        assert r['systemGasLimit']==30000000 and not r['connectedClientChainOrReleaseAcceptance']
    original=json.loads(raw)
    cases=[('initial_root','genesis state root differs'),('system_root','system post-state differs'),
        ('reader_output','getter output differs'),('account_nonce','genesis account nonce differs'),
        ('unknown_field','unknown field'),('input_digest','input digest differs')]
    with tempfile.TemporaryDirectory(prefix='system-predeploy-negative-',dir=args.input.parent) as directory:
        for name,reason in cases:
            obj=copy.deepcopy(original)
            if name=='initial_root':obj['profiles'][0]['genesisStateRoot']='0x'+'11'*32
            elif name=='system_root':obj['profiles'][0]['blocks'][0]['afterBeaconRoot']='0x'+'11'*32
            elif name=='reader_output':obj['profiles'][0]['blocks'][0]['reads'][0]['output']='0x'+'11'*32
            elif name=='account_nonce':obj['profiles'][0]['genesisAccounts'][0]['nonce']=2
            elif name=='unknown_field':obj['unreviewed']=True
            altered=json.dumps(obj,separators=(',',':')).encode() if name!='input_digest' else raw
            path=pathlib.Path(directory)/(name+'.json');path.write_bytes(altered)
            pin='00'*32 if name=='input_digest' else digest(altered)
            row=invoke(path,pin)
            assert row.returncode==101 and reason.encode() in row.stderr and not row.stdout
            print('SYSTEM-PREDEPLOY-NEGATIVE '+json.dumps({'case':name,'expectedReason':reason,'actualExit':row.returncode,
                'inputSha256':digest(altered),'expectedInputSha256':pin,'complete':True,
                'stdoutSha256':digest(row.stdout),'stderrSha256':digest(row.stderr),'refusedAtIntendedAssertion':True},sort_keys=True),flush=True)
    print('SYSTEM-PREDEPLOY-NEGATIVE-COMPLETE cases=6 complete=true nativePositiveCohortExecutions=1',flush=True)
if __name__=='__main__':main()
