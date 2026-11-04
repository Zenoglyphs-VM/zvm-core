# Canonical constructor-state recipe

`predeployState` is a consensus configuration choice. Omitted or empty retains
every existing genesis byte, nonce, slot and hash. The explicit value
`source-constructors-v1` selects a different genesis state and therefore needs
a separately accepted epoch, genesis hash and independently authorized release.
No shipped chain configuration or genesis is changed here. Unknown values and
null are refused. This is not a migration for an existing chain.

Version one starts with the existing twelve-account allocation but removes
Permit2, EntryPoint v0.7 and its SenderCreator child. The existing Arachnid
factory is preinstalled at nonce one. Two zero-value direct frames call that
complete factory runtime with the canonical salts and complete source-locked
creation bytecode: Permit2 first, then EntryPoint. The latter performs the
actual internal CREATE of SenderCreator. No signed deployment transaction,
intrinsic gas, fee accounting, sender nonce, transaction history or finality
is claimed.

The resulting genesis keeps twelve accounts and zero balances. Permit2's two
cached immutable words use the configured chain ID and canonical Permit2
address. EntryPoint has nonce two and status slot two equal to one, its child
has nonce one, and the factory has nonce three. Every other code byte and
storage slot, including configured bridge fees, remains unchanged. Existing
profiles retain the historical chain-10 Permit2 cache, EntryPoint nonce one
and empty status storage, and factory nonce one.

The fixture records complete saved compiler creation programs and zero-filled
runtime templates with immutable ranges, storage layouts and immutable Git
source pins. Its complete byte digest is hardcoded in the Go builder. This
reuses reviewed compiler material; it does not freshly execute the compilers.
All metadata is retained. Permit2's salt comes from the pinned deployment
script; EntryPoint uses the pinned Hardhat default salt. CREATE2 predicts both
canonical addresses and CREATE at EntryPoint nonce one predicts the child.

`go run ./tools/predeploy-installation-fixtures OUTPUT` creates predictions
from those original templates and source formulas before comparing complete
state roots with the production genesis builder. It executes independent
op-geth direct frames for explicit Isthmus and the additional historical-chain-10
profile. Shipped Karst profiles are never claimed to execute through op-geth.
The source-pinned OP REVM checker executes all five profiles, compares complete
runtime bytes, nonces, balances, storage and every account state-root commitment,
and verifies five read-only getters per profile. The historical-chain-10
profile executes real constructor frames in that declared synthetic context;
it does not establish the chain on which the saved deployment occurred.

The existing locked Rust build also discovers `predeploy-installation` as a
second binary, sharing the original crate's compiled dependencies and pinned
OP modules. No second workspace or compiler dependency installation is added.
`scripts/check-predeploy-installation.py` executes one positive cohort and
thirteen deliberate negative inputs with intended-assertion/exit checks. The
original Go, system-contract and complete-client controls remain mandatory.

Release tests use synthetic policies and fresh fixture signatures to bind the
selector and changed genesis. Even repinned, newly signed wrong nonces, status,
child code, factory state, historical Permit2 cache and legacy allocations are
refused. The ordinary release receipt describes the selected genesis state;
`constructorExecutionObserved` remains false because offline verification does
not run the CI constructors. It grants no real signing or operator authority.

Maintainer adoption, real release policies and signatures, Node adoption of
this Core revision, connected execution of the newly selected genesis, NoM
canonicality, owner migration and platform qualification remain separate gates.
