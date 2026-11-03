// Command connected-client-check qualifies short, synthetic connected chains
// through a complete, digest-pinned OP execution client. It has no NoM authority.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zenoglyphs-VM/zvm-core/derive"
	"github.com/Zenoglyphs-VM/zvm-core/l2"
	"github.com/Zenoglyphs-VM/zvm-core/rollup"
	"github.com/Zenoglyphs-VM/zvm-core/znn"
	"github.com/ethereum/go-ethereum/beacon/engine"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
)

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func check(ok bool, reason string) {
	if !ok {
		panic(reason)
	}
}
func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func output(label string, value any) {
	raw, err := json.Marshal(value)
	must(err)
	fmt.Printf("CONNECTED-CLIENT-%s %s\n", label, raw)
}

var depositor = common.HexToAddress("0x00000000000000000000000000000000000a11ce")

func buildPlan(cfg *rollup.Config, name string, number uint64) (*derive.Plan, *big.Int) {
	m := &znn.Momentum{Version: 1, ChainIdentifier: cfg.L1ChainID, Height: cfg.L1StartHeight + number - 1,
		Timestamp: cfg.L2GenesisTimestamp + number, Hash: znn.Sha3([]byte(fmt.Sprintf("offline-connected/%s/%d", name, number)))}
	amount := number * 100_000_000
	b := &znn.AccountBlock{Version: 1, ChainIdentifier: cfg.L1ChainID, BlockType: znn.BlockTypeUserSend,
		Height: number, Address: znn.Address(depositor), ToAddress: cfg.Vault, TokenStandard: znn.ZNN,
		Amount: znn.BigIntFromUint64(amount), BasePlasma: znn.BaseSendPlasma(0), UsedPlasma: znn.BaseSendPlasma(0)}
	b.FusedPlasma = b.BasePlasma
	b.Hash = b.ComputeHash()
	m.Content = []znn.AccountHeader{{Address: b.Address, Hash: b.Hash, Height: b.Height}}
	p, err := derive.BuildPlan(cfg, &znn.DetailedMomentum{Momentum: m, Blocks: []*znn.AccountBlock{b}}, nil)
	must(err)
	check(len(p.Txs) == 2 && p.Txs[0].Lane == derive.LaneSystem && p.Txs[1].Lane == derive.LaneDeposit, "planned lanes differ")
	mint := new(big.Int).Mul(new(big.Int).SetUint64(amount), rollup.ZNNScale)
	check(p.Txs[1].Tx.Mint().Cmp(mint) == 0 && p.Txs[1].Tx.From() == depositor, "planned mint differs")
	return p, mint
}

func exercise(ctx context.Context, name, root string) {
	path := name
	if name == "supported-isthmus" {
		path = "l2"
	}
	raw, err := os.ReadFile("chains/" + path + "/rollup.json")
	must(err)
	cfg, err := rollup.Parse(raw)
	must(err)
	if name == "supported-isthmus" {
		cfg.EVM = "isthmus"
	}
	g, err := l2.BuildGenesis(cfg, cfg.L2GenesisTimestamp)
	must(err)
	if name == "supported-isthmus" {
		cfg.L2GenesisHash = g.ToBlock().Hash()
	}
	check(g.ToBlock().Hash() == cfg.L2GenesisHash && len(g.Alloc) == 12, "source genesis differs")
	dir := filepath.Join(root, name)
	must(os.Mkdir(dir, 0700))
	genesis, err := json.Marshal(g)
	must(err)
	must(os.WriteFile(filepath.Join(dir, "genesis.json"), genesis, 0600))
	var secret [32]byte // Public, ephemeral offline fixture; not an operator credential.
	must(os.WriteFile(filepath.Join(dir, "jwt.hex"), []byte(hex.EncodeToString(secret[:])), 0600))
	model := initial(g)
	head := cfg.L2GenesisHash
	run := startClient(ctx, dir)
	defer func() { run.stop() }()
	e, err := l2.DialEngine(ctx, "http://127.0.0.1:8551", secret)
	must(err)
	defer e.Close()
	c, err := rpc.DialContext(ctx, "http://127.0.0.1:8545")
	must(err)
	defer c.Close()
	checkGenesis(ctx, c, g)
	unauthenticatedRefused(ctx)
	roots := []common.Hash{}
	hashes := []common.Hash{}
	txs := 0
	for number := uint64(1); number <= 8; number++ {
		p, mint := buildPlan(cfg, name, number)
		attrs, err := p.Attributes()
		must(err)
		fcu, err := e.ForkchoiceUpdated(ctx, engine.ForkchoiceStateV1{HeadBlockHash: head, SafeBlockHash: head, FinalizedBlockHash: head}, attrs)
		must(err)
		check(fcu.PayloadStatus.Status == "VALID" && fcu.PayloadID != nil, "payload construction refused")
		env, err := e.GetPayload(ctx, *fcu.PayloadID)
		must(err)
		payload := env.ExecutionPayload
		check(payload != nil && payload.ParentHash == head && payload.Number == number && payload.Timestamp == attrs.Timestamp, "connected header differs")
		check(payload.GasLimit == cfg.GasLimit && len(payload.Transactions) == len(attrs.Transactions), "payload limits or transaction count differ")
		for i, raw := range attrs.Transactions {
			check(string(raw) == string(payload.Transactions[i]), "forced transaction order or bytes differ")
		}
		predicted := advance(model, p, head, number, mint)
		check(payload.StateRoot == predicted, "independent complete post-state root differs")
		status, err := e.NewPayload(ctx, payload, *attrs.BeaconRoot)
		must(err)
		check(status.Status == "VALID" && status.LatestValidHash != nil && *status.LatestValidHash == payload.BlockHash, "payload import refused")
		fcu, err = e.ForkchoiceUpdated(ctx, engine.ForkchoiceStateV1{HeadBlockHash: payload.BlockHash, SafeBlockHash: payload.BlockHash, FinalizedBlockHash: payload.BlockHash}, nil)
		must(err)
		check(fcu.PayloadStatus.Status == "VALID", "canonical head update refused")
		checkStored(ctx, c, payload, p, model)
		// A changed state root with the original block hash must be refused. Never
		// retry construction or replace a refused planned transaction with a subset.
		altered := *payload
		altered.StateRoot = common.Hash{31: 1}
		bad, err := e.NewPayload(ctx, &altered, *attrs.BeaconRoot)
		must(err)
		check(bad.Status == "INVALID" || bad.Status == "INVALID_BLOCK_HASH", "altered payload was not refused")
		// Query the canonical header separately after the deliberately invalid import.
		var tip *types.Header
		must(c.CallContext(ctx, &tip, "eth_getBlockByNumber", "latest", false))
		check(tip != nil && tip.Hash() == payload.BlockHash, "negative import changed canonical head")
		head = payload.BlockHash
		roots = append(roots, predicted)
		hashes = append(hashes, head)
		txs += len(p.Txs)
		output("BLOCK", map[string]any{"profile": name, "number": number, "parentHash": payload.ParentHash, "blockHash": head, "stateRoot": predicted, "forcedTransactions": len(p.Txs), "completeStateAccounts": 14, "invalidStateRootRefused": true})
		if number == 4 {
			run.stop()
			run = startClient(ctx, dir)
			var restored *types.Header
			must(c.CallContext(ctx, &restored, "eth_getBlockByNumber", "latest", false))
			check(restored != nil && restored.Hash() == head && restored.Root == predicted, "persisted head after restart differs")
			checkAccounts(ctx, c, model)
			output("RESTART", map[string]any{"profile": name, "number": number, "blockHash": head, "stateRoot": predicted, "completeStateAccounts": 14})
		}
	}
	check(len(roots) == 8 && txs == 16, "incomplete connected cohort")
	output("PROFILE", map[string]any{"profile": name, "rules": cfg.EVM, "chainId": cfg.L2ChainID, "sourceRollupSha256": digest(raw), "genesisHash": cfg.L2GenesisHash,
		"genesisStateRoot": g.ToBlock().Root(), "connectedBlocks": 8, "exactPlannedTransactions": 16, "completeStateRoots": roots, "blockHashes": hashes,
		"completeStateAccountChecks": 126, "receiptChecks": 16, "invalidPayloadRefusals": 8, "persistedHeadRestarts": 1, "unauthenticatedEngineRefusals": 1,
		"actualCompleteClientExecuted": true, "NoMCanonicalityOrReleaseAcceptance": false})
}

func main() {
	check(len(os.Args) == 2, "one fresh qualification directory required")
	verifySandbox()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	version, err := exec.CommandContext(ctx, "/usr/local/bin/op-reth", "--version").CombinedOutput()
	must(err)
	check(len(version) < 16<<10 && strings.Contains(string(version), "2.6.1") && strings.Contains(string(version), "e16bffa"), "image-reported version differs")
	f, err := os.Open("/usr/local/bin/op-reth")
	must(err)
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, 512<<20))
	must(err)
	must(f.Close())
	check(n > 0 && n < 512<<20, "client binary size differs")
	check(n == 101744960 && hex.EncodeToString(h.Sum(nil)) == "3dc4a63aa83daa5ee015386b5e961ce09d01bb54da309af8401ccb4739cfdbc1", "pinned client executable differs")
	output("BINARY", map[string]any{"version": string(version), "sha256": hex.EncodeToString(h.Sum(nil)), "bytes": n, "independentBuildProvenance": false})
	root := os.Args[1]
	must(os.Mkdir(root, 0700))
	for _, name := range []string{"supported-isthmus", "l2", "l2-69", "l2-dp"} {
		exercise(ctx, name, root)
	}
	fmt.Println("CONNECTED-CLIENT-COMPLETE profiles=4 connectedBlocks=32 exactPlannedTransactions=64 completeStateRoots=32 persistedHeadRestarts=4 invalidPayloadRefusals=32 productionAccepted=false")
}
