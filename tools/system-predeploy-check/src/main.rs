//! Offline system-call checks. Synthetic contexts do not establish a running
//! client, connected headers, consensus authority, or an accepted release.
include!(concat!(env!("OUT_DIR"), "/op_modules.rs"));

use alloy_trie::{
    TrieAccount,
    root::{state_root_unhashed, storage_root_unhashed},
};
use revm::{
    DatabaseCommit,
    context::{BlockEnv, CfgEnv, Context, TxEnv},
    context_interface::{JournalTr, block::BlobExcessGasAndPrice, result::ExecutionResult},
    database::{AccountState, CacheDB, EmptyDB},
    handler::{Handler, MainnetHandler, SystemCallTx},
    primitives::{Address, B256, Bytes, U256, address, hex},
    state::{AccountInfo, Bytecode},
};
use serde::Deserialize;
use sha2::{Digest, Sha256};
use std::{env, fs::File, io::Read};

const BEACON: Address = address!("000f3df6d732807ef1319fb7b8bb8522d0beac02");
const HISTORY: Address = address!("0000f90827f1c53a10cb7a02335b175320002935");
const SYSTEM: Address = address!("fffffffffffffffffffffffffffffffffffffffe");
const READER: Address = address!("0000000000000000000000000000000000001234");
type Db = CacheDB<EmptyDB>;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Input {
    schema: u32,
    scope: String,
    profiles: Vec<Profile>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Profile {
    name: String,
    rules: String,
    chain_id: u64,
    rollup_sha256: String,
    genesis_hash: String,
    genesis_state_root: String,
    genesis_accounts: Vec<Account>,
    blocks: Vec<Block>,
    go_reference_executed: bool,
    go_roots: Vec<String>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Account {
    address: String,
    nonce: u64,
    balance: String,
    code: String,
    storage: Vec<Slot>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Slot {
    key: String,
    value: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Block {
    number: u64,
    timestamp: u64,
    gas_limit: u64,
    base_fee: u64,
    beacon_root: String,
    parent_hash: String,
    after_beacon_root: String,
    after_history_root: String,
    reads: Vec<ReadCheck>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ReadCheck {
    id: String,
    address: String,
    data: String,
    output: String,
    revert: bool,
}

fn sha(raw: &[u8]) -> String {
    format!("{:x}", Sha256::digest(raw))
}
fn data(value: &str) -> Vec<u8> {
    assert!(value.starts_with("0x") && value.len() % 2 == 0 && value.len() <= 2 + (1 << 21));
    hex::decode(&value[2..]).expect("invalid hexadecimal fixture")
}
fn hash(value: &str) -> B256 {
    let raw = data(value);
    assert_eq!(raw.len(), 32);
    B256::from_slice(&raw)
}
fn addr(value: &str) -> Address {
    let raw = data(value);
    assert_eq!(raw.len(), 20);
    Address::from_slice(&raw)
}
fn state_root(db: &Db) -> B256 {
    state_root_unhashed(
        db.cache
            .accounts
            .iter()
            .filter(|(_, a)| a.account_state != AccountState::NotExisting && !a.info.is_empty())
            .map(|(address, a)| {
                (
                    *address,
                    TrieAccount {
                        nonce: a.info.nonce,
                        balance: a.info.balance,
                        code_hash: a.info.code_hash,
                        storage_root: storage_root_unhashed(
                            a.storage
                                .iter()
                                .filter(|(_, v)| !v.is_zero())
                                .map(|(k, v)| (B256::from(k.to_be_bytes::<32>()), *v)),
                        ),
                    },
                )
            }),
    )
}
fn initialize(p: &Profile) -> Db {
    assert_eq!(p.genesis_accounts.len(), 12);
    let mut db = Db::default();
    let mut prior = None;
    let mut code_bytes = 0;
    for a in &p.genesis_accounts {
        let address = addr(&a.address);
        assert!(prior.is_none_or(|old| old < address));
        prior = Some(address);
        assert_eq!(a.nonce, 1, "genesis account nonce differs");
        assert_eq!(a.balance, "0");
        assert!(a.storage.len() <= 4096);
        let code = data(&a.code);
        assert!(!code.is_empty() && code.len() <= 24576);
        code_bytes += code.len();
        assert!(code_bytes <= 1 << 20);
        if address == BEACON {
            assert_eq!(
                sha(&code),
                "cb7bd3e115730f7d57c15cd892880ed517ff4421bf43dfc7591198868fffe31d"
            );
        }
        if address == HISTORY {
            assert_eq!(
                sha(&code),
                "41cd74981e0201f79ebca054c4e8102a7e6faa28ce015d0048ab2250c1222c67"
            );
        }
        let mut info = AccountInfo::default().with_code(Bytecode::new_legacy(Bytes::from(code)));
        info.nonce = a.nonce;
        db.insert_account_info(address, info);
        let mut previous = None;
        for slot in &a.storage {
            let key = U256::from_be_bytes(hash(&slot.key).0);
            assert!(previous.is_none_or(|old| old < key));
            previous = Some(key);
            db.insert_account_storage(address, key, U256::from_be_bytes(hash(&slot.value).0))
                .unwrap();
        }
    }
    assert!(db.cache.accounts.contains_key(&BEACON) && db.cache.accounts.contains_key(&HISTORY));
    assert_eq!(
        state_root(&db),
        hash(&p.genesis_state_root),
        "genesis state root differs"
    );
    db
}
fn call(
    db: &mut Db,
    p: &Profile,
    b: &Block,
    caller: Address,
    target: Address,
    raw: &[u8],
    gas: u64,
) -> (bool, bool, Vec<u8>) {
    assert!(target == BEACON || target == HISTORY);
    let spec = match p.rules.as_str() {
        "isthmus" => OpSpecId::ISTHMUS,
        "karst" => OpSpecId::KARST,
        _ => panic!("unsupported OP rules"),
    };
    let mut ctx: Context<BlockEnv, TxEnv, CfgEnv<OpSpecId>, &mut Db> = Context::new(db, spec);
    ctx.modify_cfg(|cfg| {
        cfg.chain_id = p.chain_id;
        cfg.memory_limit = 128 << 20;
    });
    ctx.block = BlockEnv {
        number: U256::from(b.number),
        timestamp: U256::from(b.timestamp),
        gas_limit: b.gas_limit,
        basefee: b.base_fee,
        prevrandao: Some(hash(&b.beacon_root)),
        blob_excess_gas_and_price: Some(BlobExcessGasAndPrice {
            excess_blob_gas: 0,
            blob_gasprice: 1,
        }),
        ..Default::default()
    };
    ctx.tx = TxEnv::new_system_tx_with_caller(caller, target, Bytes::copy_from_slice(raw));
    // Pin the EIP gas budget rather than inheriting a future-fork reservoir
    // from the general system-transaction factory.
    ctx.tx.gas_limit = gas;
    assert_eq!(ctx.tx.value, U256::ZERO);
    assert_eq!(ctx.tx.gas_price, 0);
    let mut evm = OpEvm::new(ctx, ());
    // The production REVM system handler skips admission, intrinsic gas,
    // caller deduction, nonce increments and beneficiary fee rewards.
    let result = MainnetHandler::<
        _,
        revm::context_interface::result::EVMError<core::convert::Infallible>,
        _,
    >::default()
    .run_system_call(&mut evm.0)
    .expect("actual system handler failed");
    let ok = result.is_success();
    let reverted = matches!(&result, ExecutionResult::Revert { .. });
    let output = result.output().map(|v| v.to_vec()).unwrap_or_default();
    let changes = evm.0.ctx.journaled_state.finalize();
    evm.0.ctx.journaled_state.database.commit(changes);
    (ok, reverted, output)
}
fn execute(p: &Profile) {
    assert_eq!(p.blocks.len(), 6);
    assert_eq!(p.rollup_sha256.len(), 64);
    assert_eq!(hash(&p.genesis_hash).len(), 32);
    assert_eq!(p.go_reference_executed, p.rules == "isthmus");
    assert_eq!(
        p.go_roots.len(),
        if p.go_reference_executed { 12 } else { 0 }
    );
    let mut db = initialize(p);
    let numbers = [1, 2, 8191, 8192, 8193, 16383];
    let mut roots = Vec::new();
    let mut read_ids = std::collections::BTreeSet::new();
    let mut previous_time = 0;
    for (index, b) in p.blocks.iter().enumerate() {
        assert_eq!(b.number, numbers[index]);
        assert!(b.timestamp > previous_time);
        previous_time = b.timestamp;
        assert_eq!(b.reads.len(), 10);
        assert!(b.gas_limit > 0);
        for (target, input, expected) in [
            (BEACON, hash(&b.beacon_root), hash(&b.after_beacon_root)),
            (HISTORY, hash(&b.parent_hash), hash(&b.after_history_root)),
        ] {
            let (ok, reverted, out) =
                call(&mut db, p, b, SYSTEM, target, input.as_slice(), 30_000_000);
            assert!(ok && !reverted && out.is_empty());
            assert_eq!(state_root(&db), expected, "system post-state differs");
            roots.push(format!("{:#x}", expected));
        }
        read_ids.clear();
        for r in &b.reads {
            assert!(read_ids.insert(&r.id));
            let before = state_root(&db);
            let (ok, reverted, out) = call(
                &mut db,
                p,
                b,
                READER,
                addr(&r.address),
                &data(&r.data),
                100000,
            );
            assert_eq!(reverted, r.revert);
            assert_eq!(ok, !r.revert);
            assert_eq!(out, data(&r.output), "getter output differs");
            assert_eq!(state_root(&db), before);
            for address in [SYSTEM, READER] {
                if let Some(a) = db.cache.accounts.get(&address) {
                    assert_eq!(a.info.nonce, 0);
                    assert_eq!(a.info.balance, U256::ZERO);
                }
            }
        }
    }
    if p.go_reference_executed {
        assert_eq!(roots, p.go_roots);
    }
    println!(
        "SYSTEM-PREDEPLOY-NATIVE {}",
        serde_json::json!({
            "profile":p.name,"rules":p.rules,"chainId":p.chain_id,"genesisHash":p.genesis_hash,"genesisStateRoot":p.genesis_state_root,
            "contexts":6,"actualProtocolSystemHandlerCalls":12,"systemGasLimit":30000000,"actualNonsystemGetterChecks":60,
            "completeStateRoots":roots,"independentGoReferenceMatched":p.go_reference_executed,
            "allInitialTwelveAccountNoncesBalancesCodeAndStorageCommitted":true,"systemAndReaderNonceAndBalanceUnchanged":true,
            "ringOverwriteAndReadRefusalCases":true,"connectedClientChainOrReleaseAcceptance":false,
            "opSourceCommit":env!("ZVM_OP_SOURCE_COMMIT"),"dependencyLockSha256":env!("ZVM_RUNNER_LOCK_SHA256")
        })
    );
}
fn main() {
    let args: Vec<String> = env::args().collect();
    assert_eq!(args.len(), 3);
    assert_eq!(args[2].len(), 64);
    assert!(
        args[2]
            .bytes()
            .all(|c| c.is_ascii_digit() || (b'a'..=b'f').contains(&c))
    );
    let mut raw = Vec::new();
    File::open(&args[1])
        .unwrap()
        .take((4 << 20) + 1)
        .read_to_end(&mut raw)
        .unwrap();
    assert!(!raw.is_empty() && raw.len() <= 4 << 20);
    assert_eq!(sha(&raw), args[2], "input digest differs");
    let input: Input =
        serde_json::from_slice(&raw).expect("invalid strict system-predeploy fixture");
    assert_eq!(input.schema, 1);
    assert!(input.scope.contains("synthetic block contexts"));
    assert_eq!(input.profiles.len(), 4);
    for (p, name) in input
        .profiles
        .iter()
        .zip(["supported-isthmus", "l2", "l2-69", "l2-dp"])
    {
        assert_eq!(p.name, name);
        execute(p);
    }
    println!(
        "SYSTEM-PREDEPLOY-COMPLETE profiles=4 protocolSystemCalls=48 getters=240 completeRoots=48 realReleaseAccepted=false"
    );
}
