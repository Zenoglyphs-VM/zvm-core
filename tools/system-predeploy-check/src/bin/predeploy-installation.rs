//! Canonical installation frames against source-predicted complete state. This
//! is not a historical deployment transaction, live chain or accepted release.
include!(concat!(env!("OUT_DIR"), "/op_modules.rs"));

use alloy_trie::{TrieAccount, root::{state_root_unhashed, storage_root_unhashed}};
use revm::{
    DatabaseCommit,
    context::{BlockEnv, CfgEnv, Context, TxEnv},
    context_interface::{JournalTr, block::BlobExcessGasAndPrice},
    database::{AccountState, CacheDB, EmptyDB},
    handler::{Handler, MainnetHandler, SystemCallTx},
    primitives::{Address, B256, Bytes, U256, address, hex, keccak256},
    state::{AccountInfo, Bytecode},
};
use serde::Deserialize;
use sha2::{Digest, Sha256};
use std::{env, fs::File, io::Read};

const FACTORY: Address = address!("4e59b44847b379578588920ca78fbf26c0b4956c");
const PERMIT: Address = address!("000000000022d473030f116ddee9f6b43ac78ba3");
const ENTRY: Address = address!("0000000071727de22e5e9d8baf0edac6f37da032");
const CHILD: Address = address!("efc2c1444ebcc4db75e7613d20c6a62ff67a167c");
const CALLER: Address = address!("0000000000000000000000000000000000001234");
const MATERIAL: &str = "c6d8935aa594ac0932d6b8728fa53193d0c47f33690ef217310bdf7be698ff76";
type Db = CacheDB<EmptyDB>;

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Input { schema: u32, scope: String, profiles: Vec<Profile> }
#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Profile {
    name: String, rules: String, chain_id: u64, timestamp: u64, gas_limit: u64, base_fee: u64,
    predeploy_state: String,
    #[serde(rename = "rollupSha256")]
    rollup_sha: String,
    #[serde(rename = "materialSha256")]
    material_sha: String,
    legacy_genesis_hash: String, legacy_state_root: String, candidate_genesis_hash: String, candidate_state_root: String,
    genesis_accounts: Vec<Account>, before_root: String, steps: Vec<Step>, reads: Vec<ReadCheck>,
    go_reference_executed: bool, go_roots: Vec<String>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Account { address: String, nonce: u64, balance: String, code: String, storage: Vec<Slot> }
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Slot { key: String, value: String }
#[derive(Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
struct Step {
    name: String, salt: String, creation: String,
    #[serde(rename = "creationSha256")]
    creation_sha: String,
    address: String,
    runtime: String, child_runtime: String, after_root: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ReadCheck { id: String, address: String, data: String, output: String }

fn sha(raw: &[u8]) -> String { format!("{:x}", Sha256::digest(raw)) }
fn data(value: &str) -> Vec<u8> {
    assert!(value.starts_with("0x") && value.len() % 2 == 0 && value.len() <= 2 + (1 << 21));
    hex::decode(&value[2..]).expect("invalid hexadecimal fixture")
}
fn hash(value: &str) -> B256 { let raw = data(value); assert_eq!(raw.len(), 32); B256::from_slice(&raw) }
fn addr(value: &str) -> Address { let raw = data(value); assert_eq!(raw.len(), 20); Address::from_slice(&raw) }
fn state_root(db: &Db) -> B256 {
    state_root_unhashed(db.cache.accounts.iter()
        .filter(|(_, a)| a.account_state != AccountState::NotExisting && !a.info.is_empty())
        .map(|(address, a)| (*address, TrieAccount {
            nonce: a.info.nonce, balance: a.info.balance, code_hash: a.info.code_hash,
            storage_root: storage_root_unhashed(a.storage.iter().filter(|(_, v)| !v.is_zero())
                .map(|(k, v)| (B256::from(k.to_be_bytes::<32>()), *v))),
        })))
}
fn initialize(p: &Profile) -> Db {
    assert_eq!(p.genesis_accounts.len(), 12);
    let mut db = Db::default(); let mut prior = None; let mut code_bytes = 0;
    for a in &p.genesis_accounts {
        let address = addr(&a.address); assert!(prior.is_none_or(|old| old < address)); prior = Some(address);
        assert_eq!(a.nonce, 1, "legacy account nonce differs"); assert_eq!(a.balance, "0");
        assert!(a.storage.len() <= 4096);
        let code = data(&a.code); assert!(!code.is_empty() && code.len() <= 24576);
        code_bytes += code.len(); assert!(code_bytes <= 1 << 20);
        let pin = if address == FACTORY { Some("e0af82ad2e5188285db8ba0b2ae054d13f0fd4fe325c48c7f2dace48454404b9") }
            else if address == PERMIT { Some("5b4c4aeff7672fe4493c30120610318a79c1c3129e8d0ecc7861d4867c481a3b") }
            else if address == ENTRY { Some("674d73ee2acf6d72a5b32b6e05150b9ddcfcee0429a0e7914929faccbc2bf4d5") }
            else if address == CHILD { Some("46ce0a80a81a08523313640038083c3941ebb67909ec13b6ba257b0d3dc4a1db") }
            else { None };
        if let Some(expected) = pin { assert_eq!(sha(&code), expected, "legacy runtime differs"); assert!(a.storage.is_empty()); }
        let mut info = AccountInfo::default().with_code(Bytecode::new_legacy(Bytes::from(code))); info.nonce = a.nonce;
        db.insert_account_info(address, info); let mut previous = None;
        for slot in &a.storage {
            let key = U256::from_be_bytes(hash(&slot.key).0); assert!(previous.is_none_or(|old| old < key)); previous = Some(key);
            let value = U256::from_be_bytes(hash(&slot.value).0); assert!(!value.is_zero());
            db.insert_account_storage(address, key, value).unwrap();
        }
    }
    assert_eq!(state_root(&db), hash(&p.legacy_state_root), "legacy state root differs");
    for target in [PERMIT, ENTRY, CHILD] { assert!(db.cache.accounts.remove(&target).is_some()); }
    assert!(db.cache.accounts.contains_key(&FACTORY));
    assert_eq!(db.cache.accounts.len(), 9);
    assert_eq!(state_root(&db), hash(&p.before_root), "constructor pre-state differs");
    db
}
fn call(db: &mut Db, p: &Profile, target: Address, raw: &[u8], gas: u64) -> Vec<u8> {
    assert!(target == FACTORY || target == PERMIT || target == ENTRY);
    let spec = match p.rules.as_str() { "isthmus" => OpSpecId::ISTHMUS, "karst" => OpSpecId::KARST, _ => panic!("unsupported OP rules") };
    let mut ctx: Context<BlockEnv, TxEnv, CfgEnv<OpSpecId>, &mut Db> = Context::new(db, spec);
    ctx.modify_cfg(|cfg| { cfg.chain_id = p.chain_id; cfg.memory_limit = 128 << 20; });
    ctx.block = BlockEnv {
        number: U256::from(1), timestamp: U256::from(p.timestamp), gas_limit: p.gas_limit, basefee: p.base_fee,
        prevrandao: Some(keccak256(b"canonical-constructor-state/offline")),
        blob_excess_gas_and_price: Some(BlobExcessGasAndPrice { excess_blob_gas: 0, blob_gasprice: 1 }), ..Default::default()
    };
    ctx.tx = TxEnv::new_system_tx_with_caller(CALLER, target, Bytes::copy_from_slice(raw));
    ctx.tx.gas_limit = gas; assert_eq!(ctx.tx.value, U256::ZERO); assert_eq!(ctx.tx.gas_price, 0);
    let mut evm = OpEvm::new(ctx, ());
    // Direct, zero-value frames: no transaction admission/intrinsic gas,
    // sender nonce, beneficiary fees or historical transaction is asserted.
    let result = MainnetHandler::<_, revm::context_interface::result::EVMError<core::convert::Infallible>, _>::default()
        .run_system_call(&mut evm.0).expect("direct constructor/getter frame failed");
    assert!(result.is_success(), "constructor/getter execution failed");
    let output = result.output().map(|v| v.to_vec()).unwrap_or_default();
    let changes = evm.0.ctx.journaled_state.finalize(); evm.0.ctx.journaled_state.database.commit(changes);
    output
}
fn unchanged_caller(db: &Db) {
    if let Some(a) = db.cache.accounts.get(&CALLER) {
        assert_eq!(a.info.nonce, 0); assert_eq!(a.info.balance, U256::ZERO); assert!(a.info.is_empty());
    }
}
fn code_matches(db: &Db, address: Address, expected: &[u8], reason: &str) {
    let account = db.cache.accounts.get(&address).expect("constructed account missing");
    assert_eq!(account.info.balance, U256::ZERO);
    // Both the bytecode cache and the complete account commitment must match.
    assert_eq!(account.info.code_hash, keccak256(expected), "{reason}");
    let code = db.cache.contracts.get(&account.info.code_hash).expect("constructed bytecode missing");
    assert_eq!(code.original_bytes().as_ref(), expected, "{reason}");
}
fn execute(p: &Profile) {
    assert_eq!(p.predeploy_state, "source-constructors-v1", "selected recipe differs");
    assert_eq!(p.material_sha, MATERIAL); assert_eq!(p.rollup_sha.len(), 64);
    assert!(p.chain_id > 0 && p.timestamp > 0 && p.gas_limit >= 8_000_000 && p.base_fee > 0);
    assert_ne!(hash(&p.legacy_genesis_hash), hash(&p.candidate_genesis_hash));
    assert_ne!(hash(&p.legacy_state_root), hash(&p.candidate_state_root));
    assert_eq!(p.steps.len(), 2); assert_eq!(p.reads.len(), 5);
    assert_eq!(p.go_reference_executed, p.rules == "isthmus");
    assert_eq!(p.go_roots.len(), if p.go_reference_executed { 2 } else { 0 });
    let mut db = initialize(p); let mut roots = Vec::new();
    for (index, s) in p.steps.iter().enumerate() {
        let (name, salt, expected_address, pin, length) = if index == 0 {
            ("Permit2", "0x0000000000000000000000000000000000000000d3af2663da51c10215000000", PERMIT,
             "061c1d230d740e10d9e7ff23c3bc1f918abd20c3d017188697ba205d61c666ff", 9345)
        } else {
            ("EntryPoint_v07", "0x90d8084deab30c2a37c45e8d47f49f2f7965183cb6990a98943ef94940681de3", ENTRY,
             "a227a5ba81844120bf91c337dca44002b26f51878f4a8acd9b7b3fb6a64ed054", 16650)
        };
        assert_eq!(s.name, name); assert_eq!(s.salt, salt, "canonical salt differs"); assert_eq!(addr(&s.address), expected_address);
        let creation = data(&s.creation); assert_eq!(creation.len(), length);
        assert_eq!(s.creation_sha, pin); assert_eq!(sha(&creation), pin, "complete creation digest differs");
        let mut preimage = vec![0xff]; preimage.extend_from_slice(FACTORY.as_slice()); preimage.extend_from_slice(hash(salt).as_slice()); preimage.extend_from_slice(keccak256(&creation).as_slice());
        assert_eq!(&keccak256(preimage).as_slice()[12..], expected_address.as_slice());
        let mut input = data(salt); input.extend_from_slice(&creation);
        let output = call(&mut db, p, FACTORY, &input, 8_000_000);
        assert_eq!(output, expected_address.as_slice(), "factory return address differs");
        let runtime = data(&s.runtime); assert_eq!(runtime.len(), if index == 0 { 9152 } else { 16035 });
        code_matches(&db, expected_address, &runtime, "constructed runtime differs");
        assert_eq!(db.cache.accounts[&expected_address].info.nonce, if index == 0 { 1 } else { 2 });
        assert_eq!(db.cache.accounts[&FACTORY].info.nonce, index as u64 + 2);
        if index == 0 { assert_eq!(s.child_runtime, "0x"); } else {
            let child = data(&s.child_runtime); assert_eq!(child.len(), 451);
            code_matches(&db, CHILD, &child, "constructed child runtime differs");
            assert_eq!(db.cache.accounts[&CHILD].info.nonce, 1);
            assert_eq!(db.cache.accounts[&ENTRY].storage.get(&U256::from(2)), Some(&U256::from(1)));
        }
        unchanged_caller(&db);
        assert_eq!(state_root(&db), hash(&s.after_root), "constructor post-state differs");
        roots.push(format!("{:#x}", state_root(&db)));
    }
    assert_eq!(state_root(&db), hash(&p.candidate_state_root), "selected genesis state differs");
    let mut ids = std::collections::BTreeSet::new();
    for r in &p.reads {
        assert!(ids.insert(&r.id)); let before = state_root(&db);
        let output = call(&mut db, p, addr(&r.address), &data(&r.data), 200_000);
        assert_eq!(output, data(&r.output), "constructor getter output differs");
        assert_eq!(state_root(&db), before); unchanged_caller(&db);
    }
    assert_eq!(ids.into_iter().map(String::as_str).collect::<Vec<_>>(),
        ["current-chain-domain", "empty-deposit-0", "empty-deposit-1", "empty-nonce-0", "empty-nonce-1"]);
    if p.go_reference_executed { assert_eq!(roots, p.go_roots); }
    println!("CONSTRUCTOR-STATE-NATIVE {}", serde_json::json!({
        "profile":p.name,"rules":p.rules,"chainId":p.chain_id,"predeployState":p.predeploy_state,
        "canonicalFactoryFrames":2,"actualInternalSenderCreatorCreates":1,"readOnlyGetterChecks":5,
        "completeStateRoots":roots,"candidateStateRoot":p.candidate_state_root,"candidateGenesisHash":p.candidate_genesis_hash,
        "completeStateMatchesProductionGenesis":true,"allTwelveAccountsCommitted":true,"fullRuntimeBytesCompared":true,
        "factoryNonce":3,"entryPointNonce":2,"entryPointStatusSlot":"2","entryPointStatusValue":"1","senderCreatorNonce":1,
        "callerNonceBalanceAndGetterRootsUnchanged":true,"independentGoReferenceMatched":p.go_reference_executed,
        "historicalDeploymentTransactionsOrAcceptedRelease":false,"opSourceCommit":env!("ZVM_OP_SOURCE_COMMIT"),"dependencyLockSha256":env!("ZVM_RUNNER_LOCK_SHA256")
    }));
}
fn main() {
    let args: Vec<String> = env::args().collect(); assert_eq!(args.len(), 3);
    assert_eq!(args[2].len(), 64); assert!(args[2].bytes().all(|c| c.is_ascii_digit() || (b'a'..=b'f').contains(&c)));
    let mut raw = Vec::new(); File::open(&args[1]).unwrap().take((4 << 20) + 1).read_to_end(&mut raw).unwrap();
    assert!(!raw.is_empty() && raw.len() <= 4 << 20); assert_eq!(sha(&raw), args[2], "input digest differs");
    let input: Input = serde_json::from_slice(&raw).expect("invalid strict constructor-state fixture");
    assert_eq!(input.schema, 1); assert!(input.scope.starts_with("Canonical CREATE2 direct constructor frames;")); assert_eq!(input.profiles.len(), 5);
    for (p, name) in input.profiles.iter().zip(["supported-isthmus", "l2", "l2-69", "l2-dp", "historical-chain-10"]) {
        assert_eq!(p.name, name); assert_eq!(p.rules, if name == "supported-isthmus" || name == "historical-chain-10" { "isthmus" } else { "karst" });
        if name == "historical-chain-10" { assert_eq!(p.chain_id, 10); }
        execute(p);
    }
    println!("CONSTRUCTOR-STATE-COMPLETE profiles=5 canonicalFactoryFrames=10 internalSenderCreatorCreates=5 getters=25 completeRoots=10 independentGoProfiles=2 acceptedRelease=false");
}
