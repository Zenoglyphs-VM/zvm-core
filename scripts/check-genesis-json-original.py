"""Check the exact original epoch verifier under the new regression controls.

Only the declared JSON guard is removed in this owned hosted checkout. The
result must equal the pinned parent file, and the current file is always restored.
This is an original-file regression control, not a parent-workflow qualification.
"""
from pathlib import Path
import hashlib,json,os,selectors,signal,subprocess,sys,time

ROOT=Path(__file__).resolve().parents[1]
TARGET=ROOT/'release/epoch.go'
PARENT='44224f8b05769e4691123edf0192131933cc4efe'
ORIGINAL_SHA256='45e49ea98652ff10ae00336d0420923f766be71c21c408c9596f8bb3cebd2384'
GUARD=b'\tif compilerJSON(raw, 16<<20) != nil {\n\t\treturn errors.New("L2 genesis JSON is ambiguous or exceeds its structure bounds")\n\t}\n'
TEST='TestReleaseGenesisJSONIsUnambiguous'
CASES=['root_timestamp','root_config','root_alloc','root_nonce','config_chain_id','config_optimism','optimism_elasticity',
    'allocation_account','account_code','account_nonce','account_balance','escaped_root_timestamp','ignored_nested_member',
    'invalid_utf8_ignored_value','excessive_ignored_nesting','excessive_ignored_structure']
CAP=8<<20

def digest(raw):return hashlib.sha256(raw).hexdigest()

def original_control():
    current=TARGET.read_bytes()
    assert current.count(GUARD)==1
    original=current.replace(GUARD,b'',1)
    assert digest(original)==ORIGINAL_SHA256
    output=bytearray();process=None;complete=False;exit_code=None
    began=time.monotonic();failure=None
    TARGET.write_bytes(original)
    try:
        assert TARGET.read_bytes()==original
        process=subprocess.Popen(['go','test','-p=2','-json','-count=1','-timeout=180s','-run','^'+TEST+'$','./release'],
            cwd=ROOT,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,start_new_session=True)
        selector=selectors.DefaultSelector();selector.register(process.stdout,selectors.EVENT_READ)
        try:
            while selector.get_map():
                if time.monotonic()-began>210:raise RuntimeError('Original-file control exceeded its time bound')
                for key,_ in selector.select(0.25):
                    chunk=os.read(key.fileobj.fileno(),65536)
                    if not chunk:
                        selector.unregister(key.fileobj);continue
                    if len(output)+len(chunk)>CAP:raise RuntimeError('Original-file control exceeded its output bound')
                    output.extend(chunk)
            exit_code=process.wait(timeout=max(1,210-(time.monotonic()-began)));complete=True
        finally:selector.close()
    except BaseException as error:
        failure=error
        if process is not None and process.poll() is None:
            os.killpg(process.pid,signal.SIGKILL);exit_code=process.wait(timeout=10)
    finally:
        TARGET.write_bytes(current)
        assert TARGET.read_bytes()==current
        sys.stdout.buffer.write(output);sys.stdout.buffer.flush()
    if failure:raise failure
    assert complete and exit_code==1
    rows=[]
    for line in output.splitlines():
        if line.startswith(b'{'):
            row=json.loads(line)
            if isinstance(row,dict):rows.append(row)
    ended={};chunks={}
    for row in rows:
        name=row.get('Test')
        if not isinstance(name,str):continue
        if row.get('Action')=='output':chunks[name]=chunks.get(name,'')+row.get('Output','')
        if row.get('Action') in ['pass','fail','skip']:
            assert name not in ended;ended[name]=row['Action']
    expected={TEST}|{TEST+'/'+name for name in CASES}
    assert set(ended)==expected and set(ended.values())=={'fail'}
    for name in CASES:
        text=chunks[TEST+'/'+name]
        assert text.count('GENESIS-JSON-PRECONDITIONS pass=true fixtureSignatures=2 files=7 sameDecodedHeaderAndRules=true')==1
        assert text.count('ambiguous genesis JSON was accepted')==1
        assert 'GENESIS-JSON-CONTROL pass=true' not in text
    print('GENESIS-JSON-ORIGINAL '+json.dumps({'parent':PARENT,'originalEpochSourceSHA256':ORIGINAL_SHA256,
        'currentEpochSourceSHA256':digest(current),'actualExit':exit_code,'complete':complete,
        'selectedControlCases':CASES,'expectedNamedFailures':len(expected),'allFailuresAtIntendedAcceptanceAssertion':True,
        'currentEpochSourceRestored':True,'entireParentWorkflowOrReleaseAccepted':False},sort_keys=True))

if __name__=='__main__':original_control()
