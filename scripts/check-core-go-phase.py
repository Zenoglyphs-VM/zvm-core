"""Run the original Go commands once with an enforced sampled disk budget."""
import hashlib,json,os,shlex,shutil,signal,subprocess,sys,threading,time

FLOOR=10<<30
BUDGET=3<<30
STDOUT_CAP=4<<20
STDERR_CAP=512<<10
PHASE_OUTPUT_CAP=8<<20
STEPS=[
 (['go','build','-p=2','./...'],{}),
 (['go','vet','-p=2','./...'],{}),
 (['python3','-B','scripts/check-genesis-json-original.py'],{}),
 (['go','test','-p=2','-json','-count=1','./...'],{}),
 (['go','test','-p=2','-race','-json','-count=1','-timeout=5m','./release/...'],{}),
 (['go','run','./tools/system-predeploy-fixtures','$RUNNER_TEMP/system-predeploy-input.json'],{}),
 (['go','build','-p=2','-trimpath','-o','$RUNNER_TEMP/connected-client-check','./tools/connected-client-check'],{'CGO_ENABLED':'0'}),
]

def capacity(baseline):
    free=shutil.disk_usage(os.environ['RUNNER_TEMP']).free
    if free<FLOOR:return free,'DISK_FREE_FLOOR'
    if baseline-free>BUDGET:return free,'GO_PHASE_ALLOCATION_BUDGET'
    return free,None

def execute(index,template,overrides,baseline,deadline,total):
    argv=[os.path.expandvars(part) for part in template]
    free,reason=capacity(baseline);record={'index':index,'templateArgv':template,'environmentOverrides':overrides,
        'actualArgvSha256':hashlib.sha256(json.dumps(argv,separators=(',',':')).encode()).hexdigest(),
        'actualExit':None,'complete':False,'launches':0,'reason':reason,'minimumObservedFreeBytes':free,
        'maximumObservedFreeSpaceDrawdownBytes':max(0,baseline-free)}
    if reason:return record
    # The label describes this actual child launch; the terminal record below
    # binds its real exit, complete streams and environment override.
    print('##[group]Run '+shlex.join(template),flush=True)
    try:process=subprocess.Popen(argv,env={**os.environ,**overrides},stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
    except OSError as error:
        record['reason']='START_REFUSED_'+type(error).__name__;print('##[endgroup]',flush=True);return record
    record['launches']=1;lock=threading.Lock();errors=[];observed={}
    def read(name,pipe,cap):
        count=0;h=hashlib.sha256();complete=False
        try:
            while True:
                chunk=pipe.read(4096)
                if not chunk:complete=True;break
                count+=len(chunk);h.update(chunk)
                with lock:
                    total[0]+=len(chunk)
                    if count>cap or total[0]>PHASE_OUTPUT_CAP:raise RuntimeError('OUTPUT_LIMIT')
                    sys.stdout.buffer.write(chunk);sys.stdout.buffer.flush()
        except BaseException as error:
            with lock:errors.append(str(error))
        finally:observed[name]={'bytes':count,'sha256':h.hexdigest(),'complete':complete}
    readers=[threading.Thread(target=read,args=(name,pipe,cap),daemon=True)
        for name,pipe,cap in [('stdout',process.stdout,STDOUT_CAP),('stderr',process.stderr,STDERR_CAP)]]
    for reader in readers:reader.start()
    while process.poll() is None:
        free,reason=capacity(baseline);record['minimumObservedFreeBytes']=min(record['minimumObservedFreeBytes'],free)
        record['maximumObservedFreeSpaceDrawdownBytes']=max(record['maximumObservedFreeSpaceDrawdownBytes'],baseline-free)
        if not reason and errors:reason='OUTPUT_LIMIT'
        if not reason and time.monotonic()>=deadline:reason='GO_PHASE_DEADLINE'
        if reason:
            record['reason']=reason
            try:os.killpg(process.pid,signal.SIGKILL)
            except ProcessLookupError:pass
            break
        time.sleep(.1)
    try:process.wait(timeout=15)
    except subprocess.TimeoutExpired:record['reason']='UNKNOWN_PROCESS_STATE'
    for reader in readers:reader.join(timeout=5)
    if errors and record['reason']is None:record['reason']='OUTPUT_LIMIT'
    record.update(actualExit=process.returncode,streams=observed)
    record['complete']=process.returncode is not None and not any(r.is_alive() for r in readers) and not errors and all(r['complete'] for r in observed.values()) and len(observed)==2 and record['reason']is None
    print('\nGO-PHASE-COMMAND '+json.dumps(record,sort_keys=True),flush=True)
    print('##[endgroup]',flush=True)
    return record

def main():
    baseline=shutil.disk_usage(os.environ['RUNNER_TEMP']).free
    print('GO-PHASE-ADMISSION '+json.dumps({'freeBytes':baseline,'minimumFreeBytes':FLOOR,'expectedGoBytes':BUDGET,
        'footprintBasis':'Earlier exact-source free-space drawdown after Go output preparation was 2320666624 bytes; transient peak was not measured. This new budget is enforced during all commands.'},sort_keys=True),flush=True)
    assert baseline>=FLOOR+BUDGET,'Go phase admission refused'
    deadline=time.monotonic()+480;total=[0];records=[]
    for index,(template,overrides) in enumerate(STEPS):
        row=execute(index,template,overrides,baseline,deadline,total);records.append(row)
        assert row['actualExit']==0 and row['complete'] and row['launches']==1,('actual Go phase failure',row)
    print('GO-PHASE-COMPLETE '+json.dumps({'commands':len(records),'allActualExitsZero':True,'allStreamsComplete':True,
        'actualCommandLaunches':sum(r['launches'] for r in records),'manualRetries':0,'minimumObservedFreeBytes':min(r['minimumObservedFreeBytes'] for r in records),
        'maximumObservedFreeSpaceDrawdownBytes':max(r['maximumObservedFreeSpaceDrawdownBytes'] for r in records),
        'sampleIntervalMilliseconds':100,'OSAggregateDiskQuotaClaimed':False},sort_keys=True),flush=True)
if __name__=='__main__':main()
