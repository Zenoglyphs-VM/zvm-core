# Offline system predeploy qualification

This lane checks the installed EIP-4788 and EIP-2935 runtime programs through
real protocol system-call APIs, starting from the complete supported genesis.
It covers supported Isthmus and each of the three shipped Karst configurations.

The Go fixture command calls `derive.BuildPlan` and `Plan.Attributes` for every
synthetic momentum and uses their actual beacon-root, timestamp and gas-limit
fields. Six selected contexts cover block numbers 1, 2, 8191, 8192, 8193 and
16383, including both circular-buffer overwrite boundaries. They deliberately
do not construct the intervening headers or authenticate a NoM chain. Only the
first parent hash is the genesis hash; later parent hashes are declared synthetic
inputs. The planned user/deposit transactions are not applied by this tool.

An independent specification model writes the two beacon timestamp/root slots
and the history parent-hash slot. It predicts the entire world-state root after
each hook while retaining every original account, nonce, balance, complete code
and configured bridge slot. The model is never filled from observed VM results.
On Isthmus, the command separately executes the production op-geth
`ProcessBeaconBlockRoot` and `ProcessParentBlockHash` functions and compares all
twelve roots. Go execution of Karst remains unavailable in this lane.

The Rust checker uses the exact OP spec mapping, instruction wrapper and
precompiles pinned in `upstream-source.lock.json`, and the registry dependency
checksums in `Cargo.lock`. It invokes REVM's actual `Handler::run_system_call`
through the unchanged OP wrapper. Each hook explicitly gets 30,000,000 gas,
zero value and the protocol system caller. This avoids inheriting a future-fork
state-gas reservoir from the generic system-transaction factory. Transaction
admission, intrinsic gas, nonce increments, fee deduction and beneficiary rewards
are skipped by that protocol handler. This is distinct from the earlier ordinary
constructor/CALL frame runner and leaves its schemas and historical proofs intact.

All four native profiles compare twelve full state roots and sixty getter
checks each. The cases include current roots, overwritten timestamps, zero
timestamps, malformed input lengths, current/future and expired history requests,
and attempted writes by a normal caller. Every getter checks exact output or
EVM REVERT and preserves the complete persistent root. The system and reader
accounts retain zero nonce and balance. Isthmus also matches the independent Go
hook results. No independent Go/Karst comparison is claimed.

The positive native cohort runs once. Six distinct negative inputs must fail at
their intended predicates: initial root, post-hook root, getter output, account
nonce, unknown field and input digest. The controller retains each actual exit,
complete stdout/stderr and digest in CI output. These are deliberate refusal
controls, not manual CI retries or repeats of a completed positive cohort.

Run from the repository root with Go 1.25 and Rust 1.95:

```sh
go run ./tools/system-predeploy-fixtures /approved/scratch/system-input.json
python3 -B scripts/acquire-system-predeploy-sources.py /approved/scratch/op-source
ZVM_OP_REVM_SOURCES=/approved/scratch/op-source \
  cargo build --release --locked --jobs 2 --manifest-path tools/system-predeploy-check/Cargo.toml
python3 -B scripts/check-system-predeploys.py \
  --input /approved/scratch/system-input.json \
  --binary /approved/target/release/zvm-system-predeploy-check
```

Select approved, bounded scratch and build directories for the environment.
The hosted lane uses its ephemeral runner directory, checks a 10 GiB free-space
floor plus 2 GiB expected allocation, limits compilation to two workers and
allows at most fifteen minutes for the job. Inputs are capped at 4 MiB; each
of the three public upstream source downloads is capped at 128 KiB and verified
by SHA-256. The native process has a thirty-second controller deadline and
128 MiB per-interpreter memory cap. It accepts only the two pinned runtime
digests as executable targets. No binary or artifact archive is published.

Protocol definitions: [EIP-4788](https://eips.ethereum.org/EIPS/eip-4788) and
[EIP-2935](https://eips.ethereum.org/EIPS/eip-2935). OP modules are from the
[locked Optimism source](https://github.com/ethereum-optimism/optimism/tree/e16bffa18420c86dbab693097bc33117f74a5f7d/rust/op-revm/src).
Preserve upstream and dependency license notices if distributing a binary.

This establishes bounded system-hook behavior under declared synthetic contexts.
Full op-reth block construction/import, connected-chain parent authority,
NoM canonicality/finality, signed transactions, deposits, economic behavior,
canonical deployment recipes, real release inventory and owner acceptance remain
separate. Earlier constructor witnesses and signed build policy retain their own
source and context pins. Production remains NO_GO.
