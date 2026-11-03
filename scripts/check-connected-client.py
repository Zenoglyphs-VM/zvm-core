"""Run the exact complete-client fixture once, without container networking."""
import hashlib,json,os,pathlib,shutil,subprocess,sys,threading,time

ROOT=pathlib.Path(__file__).resolve().parents[1]
lock=json.loads((ROOT/'tools/connected-client-check/image.lock.json').read_bytes())
image=lock['repository']+'@'+lock['platformManifestDigest']
root=pathlib.Path(os.environ['RUNNER_TEMP'])/'connected-client-qualification'
root.mkdir(mode=0o700)
assert shutil.disk_usage(root).free >= 12<<30
def run(argv,cap=128<<10,timeout=90):
    result=subprocess.run(argv,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,timeout=timeout)
    assert len(result.stdout)<=cap,'command output cap exceeded'
    sys.stdout.buffer.write(result.stdout);sys.stdout.buffer.flush()
    assert result.returncode==0,('command failed',result.returncode,argv[0])
    return result.stdout

run(['docker','pull','--platform','linux/amd64',image],timeout=120)
details=json.loads(run(['docker','image','inspect',image]))
assert len(details)==1
details=details[0]
assert details['Id']==lock['configurationDigest']
assert details['Architecture']=='amd64' and details['Os']=='linux'
assert details['RootFS']['Layers']==lock['uncompressedLayerDigests']
assert details['Config']['Entrypoint']==['/usr/local/bin/op-reth']
print('CONNECTED-CLIENT-IMAGE '+json.dumps({'image':image,'configurationDigest':details['Id'],
    'platform':'linux/amd64','compressedLayerBytes':lock['compressedLayerBytes'],
    'sourceCommitDeclaredByRelease':lock['sourceCommit'],'independentBuildProvenance':False},sort_keys=True),flush=True)
binary=pathlib.Path(sys.argv[1]).resolve();assert binary.is_file() and binary.stat().st_size<=128<<20
print('CONNECTED-CLIENT-DRIVER '+json.dumps({'sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'bytes':binary.stat().st_size},sort_keys=True),flush=True)
data=root/'data';data.mkdir(mode=0o700)
name='zvm-connected-client-'+os.environ['GITHUB_RUN_ID']+'-'+os.environ['GITHUB_RUN_ATTEMPT']
argv=['docker','run','--name',name,'--user',str(os.getuid())+':'+str(os.getgid()),'--network','none','--read-only','--cap-drop','ALL','--security-opt','no-new-privileges',
    '--pids-limit','256','--cpus','2','--memory','4g','--memory-swap','4g','--tmpfs','/tmp:rw,nosuid,nodev,size=256m',
    '--mount','type=bind,src='+str(ROOT/'chains')+',dst=/source/chains,readonly',
    '--mount','type=bind,src='+str(binary)+',dst=/qualification,readonly',
    '--mount','type=bind,src='+str(data)+',dst=/data','--workdir','/source','--env','GOMAXPROCS=2',
    '--env','HOME=/data/home','--env','XDG_CACHE_HOME=/data/cache','--entrypoint','/qualification',image,'/data/chains']
process=subprocess.Popen(argv,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
captured=bytearray();failures=[]
def read():
    try:
        while True:
            chunk=process.stdout.read(4096)
            if not chunk:break
            if len(captured)+len(chunk)>1<<20:raise RuntimeError('complete-client output cap exceeded')
            captured.extend(chunk);sys.stdout.buffer.write(chunk);sys.stdout.buffer.flush()
    except BaseException as error:failures.append(str(error))
reader=threading.Thread(target=read,daemon=True);reader.start();deadline=time.monotonic()+420
try:
    while process.poll() is None:
        if failures:raise RuntimeError(failures[0])
        assert time.monotonic()<deadline,'complete-client wall deadline exceeded'
        assert shutil.disk_usage(root).free>=10<<30,'hosted free-space floor reached'
        allocated=0
        for directory,subdirs,names in os.walk(data,followlinks=False):
            for part in subdirs:
                assert not (pathlib.Path(directory)/part).is_symlink(),'unexpected generated-data link'
            for part in names:
                entry=(pathlib.Path(directory)/part).lstat()
                assert not __import__('stat').S_ISLNK(entry.st_mode),'unexpected generated-data link'
                allocated+=entry.st_blocks*512
        assert allocated<=512<<20,'generated client-data allocation cap exceeded'
        time.sleep(.5)
    reader.join(timeout=5);assert not reader.is_alive() and not failures
    assert process.returncode==0,('actual complete-client exit',process.returncode)
    text=bytes(captured).decode()
    assert text.count('CONNECTED-CLIENT-COMPLETE profiles=4 connectedBlocks=32 exactPlannedTransactions=64 completeStateRoots=32 persistedHeadRestarts=4 invalidPayloadRefusals=32 productionAccepted=false')==1
    print('CONNECTED-CLIENT-TRANSPORT '+json.dumps({'actualExit':process.returncode,'complete':True,
        'bytes':len(captured),'sha256':hashlib.sha256(captured).hexdigest(),'network':'none','positiveCohortExecutions':1},sort_keys=True),flush=True)
finally:
    if process.poll() is None:
        subprocess.run(['docker','kill',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=20)
        process.wait(timeout=20)
    # The container and its generated database are retained in this ephemeral
    # hosted job for diagnostics. No local or historical evidence is deleted.
