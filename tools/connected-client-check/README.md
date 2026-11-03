# Complete OP client qualification

This opt-in CI control starts the complete official OP execution client inside
a Linux/amd64 container with no network interfaces beyond loopback. It uses a
platform manifest digest, configuration digest and layer digests from
`image.lock.json`; tags are not execution identities. The official release is
[`op-reth/v2.6.1`](https://github.com/ethereum-optimism/optimism/releases/tag/op-reth/v2.6.1).
The client reports its embedded version and its actual binary SHA-256. These
checks do not independently reproduce the image build or authenticate a release.

The source builds four genesis profiles: supported Isthmus and the three shipped
Karst configurations. Each profile imports eight connected blocks through the
production `l2.Engine` methods. Every block includes exactly the transactions
produced by `derive.BuildPlan`: the L1 information deposit and one synthetic ZNN
mint deposit. Genesis has all twelve original accounts and zero initial funds;
the source genesis hash, complete installed code and all subsequent transaction
bytes/order are checked. Gas limits and fork configuration are not reduced.

An independent state model applies the two protocol ring-buffer rules, packed
L1Block storage and deposit nonce/mint rules without an EVM or RPC simulation.
Every payload must match the predicted complete world-state root. Fourteen
accounts are checked through account RPCs at each imported head and after a
persisted restart, including balances, nonces, code hashes and storage roots.
Receipts must succeed at their exact block and transaction index. Each altered
payload root is refused without changing the accepted head. Unauthenticated
Engine API access is refused. No invalid forced transaction is dropped or retried.

CI builds a static qualification driver with the repository's pinned Go module
graph. It runs the positive cohort once, with two CPUs, a 4 GiB memory limit,
256 processes, read-only source/image, bounded diagnostics and a seven-minute
deadline. Admission reserves 4 GiB above a 10 GiB free-space floor; the floor is
monitored during execution. Client databases are private ephemeral CI fixtures.
The image is pulled only in hosted CI; local source review does not pull it or
start Docker. The original complete Go, race and native system-hook lanes remain.

These selected synthetic deposits do not establish NoM authority or finality,
historical deployment, constructor installation equivalence, economic/wallet/DEX
flows, signed transaction admission, a second independent client, default-scale
workloads, operating-system portability, owner acceptance, or a production release.
Container execution is qualification, not an operator deployment or a change to
the shipped client-image defaults. Production acceptance remains `NO_GO`.
