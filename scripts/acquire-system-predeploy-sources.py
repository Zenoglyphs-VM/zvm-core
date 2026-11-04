"""Fetch only the three public OP modules named by the checked source lock."""
import hashlib,json,os,pathlib,sys,urllib.request
def main():
    assert len(sys.argv)==2
    root=pathlib.Path(__file__).resolve().parents[1]
    lock=json.loads((root/'tools/system-predeploy-check/upstream-source.lock.json').read_bytes())
    assert lock['repository']=='https://github.com/ethereum-optimism/optimism'
    assert lock['commit']=='e16bffa18420c86dbab693097bc33117f74a5f7d'
    assert lock['sourceDirectory']=='rust/op-revm/src'
    assert set(lock['files'])=={'spec.rs','evm.rs','precompiles.rs'}
    destination=pathlib.Path(sys.argv[1]);destination.mkdir(mode=0o700,exist_ok=False)
    for name,digest in sorted(lock['files'].items()):
        url=lock['repository'].replace('https://github.com/','https://raw.githubusercontent.com/')+'/'+lock['commit']+'/'+lock['sourceDirectory']+'/'+name
        with urllib.request.urlopen(url,timeout=30) as response:raw=response.read((128<<10)+1)
        assert 0<len(raw)<=128<<10 and hashlib.sha256(raw).hexdigest()==digest
        fd=os.open(destination/name,os.O_WRONLY|os.O_CREAT|os.O_EXCL,0o600)
        with os.fdopen(fd,'wb') as handle:handle.write(raw)
        print('SYSTEM-PREDEPLOY-SOURCE '+json.dumps({'name':name,'sha256':digest,'bytes':len(raw),'sourceCommit':lock['commit']},sort_keys=True))
if __name__=='__main__':main()
