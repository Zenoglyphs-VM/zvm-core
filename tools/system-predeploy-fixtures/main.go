// Command system-predeploy-fixtures prepares source-bound offline checks for
// the two protocol system contracts. Its synthetic block contexts are not a
// connected execution chain or a NoM canonicality proof.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"sort"

	"github.com/Zenoglyphs-VM/zvm-core/derive"
	"github.com/Zenoglyphs-VM/zvm-core/l2"
	"github.com/Zenoglyphs-VM/zvm-core/rollup"
	"github.com/Zenoglyphs-VM/zvm-core/znn"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

const window = uint64(8191)

type slot struct {
	Key   common.Hash `json:"key"`
	Value common.Hash `json:"value"`
}
type account struct {
	Address common.Address `json:"address"`
	Nonce   uint64         `json:"nonce"`
	Balance string         `json:"balance"`
	Code    hexutil.Bytes  `json:"code"`
	Storage []slot         `json:"storage"`
}
type read struct {
	ID      string         `json:"id"`
	Address common.Address `json:"address"`
	Data    hexutil.Bytes  `json:"data"`
	Output  hexutil.Bytes  `json:"output"`
	Revert  bool           `json:"revert"`
}
type block struct {
	Number           uint64      `json:"number"`
	Timestamp        uint64      `json:"timestamp"`
	GasLimit         uint64      `json:"gasLimit"`
	BaseFee          uint64      `json:"baseFee"`
	BeaconRoot       common.Hash `json:"beaconRoot"`
	ParentHash       common.Hash `json:"parentHash"`
	AfterBeaconRoot  common.Hash `json:"afterBeaconRoot"`
	AfterHistoryRoot common.Hash `json:"afterHistoryRoot"`
	Reads            []read      `json:"reads"`
}
type profile struct {
	Name                string        `json:"name"`
	Rules               string        `json:"rules"`
	ChainID             uint64        `json:"chainId"`
	RollupSHA256        string        `json:"rollupSha256"`
	GenesisHash         common.Hash   `json:"genesisHash"`
	GenesisStateRoot    common.Hash   `json:"genesisStateRoot"`
	GenesisAccounts     []account     `json:"genesisAccounts"`
	Blocks              []block       `json:"blocks"`
	GoReferenceExecuted bool          `json:"goReferenceExecuted"`
	GoRoots             []common.Hash `json:"goRoots"`
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func check(ok bool) {
	if !ok {
		panic("source-bound system-predeploy qualification refused")
	}
}
func word(n uint64) common.Hash { return common.BigToHash(new(big.Int).SetUint64(n)) }
func digest(raw []byte) string {
	v := sha256.Sum256(raw)
	return hex.EncodeToString(v[:])
}
func initial(g *core.Genesis) *state.StateDB {
	st, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	must(err)
	for address, a := range g.Alloc {
		st.SetBalance(address, uint256.MustFromBig(a.Balance), tracing.BalanceIncreaseGenesisBalance)
		st.SetNonce(address, a.Nonce, tracing.NonceChangeGenesis)
		st.SetCode(address, a.Code, tracing.CodeChangeGenesis)
		for key, value := range a.Storage {
			st.SetState(address, key, value)
		}
	}
	check(st.IntermediateRoot(true) == g.ToBlock().Root() && st.Error() == nil)
	return st
}
func addRead(b *block, model *state.StateDB, id string, address common.Address, data []byte) {
	r := read{ID: id, Address: address, Data: bytes.Clone(data), Output: []byte{}}
	if len(data) != 32 {
		r.Revert = true
	} else {
		n := new(big.Int).SetBytes(data)
		if !n.IsUint64() {
			r.Revert = true
		} else if number := n.Uint64(); address == params.BeaconRootsAddress {
			r.Revert = number == 0 || model.GetState(address, word(number%window)) != word(number)
			if !r.Revert {
				value := model.GetState(address, word(number%window+window))
				r.Output = bytes.Clone(value[:])
			}
		} else {
			r.Revert = number >= b.Number || b.Number-number > window
			if !r.Revert {
				value := model.GetState(address, word(number%window))
				r.Output = bytes.Clone(value[:])
			}
		}
	}
	b.Reads = append(b.Reads, r)
}
func reference(cfg *rollup.Config, g *core.Genesis, p *profile) {
	check(!cfg.IsKarst())
	st := initial(g)
	caller := common.HexToAddress("0x1234")
	for _, b := range p.Blocks {
		e := vm.NewEVM(vm.BlockContext{CanTransfer: core.CanTransfer, Transfer: core.Transfer,
			GetHash:     func(uint64) common.Hash { panic("system contracts must not request BLOCKHASH") },
			BlockNumber: new(big.Int).SetUint64(b.Number), Time: b.Timestamp, GasLimit: b.GasLimit,
			BaseFee: new(big.Int).SetUint64(b.BaseFee), BlobBaseFee: big.NewInt(1), Difficulty: new(big.Int), Random: &b.BeaconRoot},
			st, l2.ChainConfig(cfg), vm.Config{})
		// These are the pinned production op-geth block-preprocessing hooks.
		core.ProcessBeaconBlockRoot(b.BeaconRoot, e)
		check(st.IntermediateRoot(true) == b.AfterBeaconRoot)
		p.GoRoots = append(p.GoRoots, b.AfterBeaconRoot)
		core.ProcessParentBlockHash(b.ParentHash, e)
		check(st.IntermediateRoot(true) == b.AfterHistoryRoot)
		p.GoRoots = append(p.GoRoots, b.AfterHistoryRoot)
		for _, r := range b.Reads {
			before := st.IntermediateRoot(true)
			st.Prepare(l2.ChainConfig(cfg).Rules(new(big.Int).SetUint64(b.Number), true, b.Timestamp), caller, common.Address{}, &r.Address, nil, nil)
			e.SetTxContext(vm.TxContext{Origin: caller, GasPrice: new(uint256.Int)})
			out, _, err := e.Call(caller, r.Address, r.Data, 100000, common.U2560)
			check((err == nil && !r.Revert || err == vm.ErrExecutionReverted && r.Revert) && bytes.Equal(out, r.Output))
			st.Finalise(true)
			check(st.IntermediateRoot(true) == before && st.Error() == nil && st.GetNonce(params.SystemAddress) == 0 && st.GetNonce(caller) == 0)
		}
	}
	p.GoReferenceExecuted = true
}
func build(name string) profile {
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
		g, err := l2.BuildGenesis(cfg, cfg.L2GenesisTimestamp)
		must(err)
		cfg.L2GenesisHash = g.ToBlock().Hash()
		raw, err = json.Marshal(cfg)
		must(err)
	}
	g, err := l2.BuildGenesis(cfg, cfg.L2GenesisTimestamp)
	must(err)
	check(g.ToBlock().Hash() == cfg.L2GenesisHash && len(g.Alloc) == 12)
	p := profile{Name: name, Rules: cfg.EVM, ChainID: cfg.L2ChainID, RollupSHA256: digest(raw),
		GenesisHash: g.ToBlock().Hash(), GenesisStateRoot: g.ToBlock().Root(), GenesisAccounts: []account{}, Blocks: []block{}, GoRoots: []common.Hash{}}
	for address, a := range g.Alloc {
		check(a.Balance.Sign() == 0 && a.Nonce == 1)
		out := account{Address: address, Nonce: a.Nonce, Balance: "0", Code: bytes.Clone(a.Code), Storage: []slot{}}
		for key, value := range a.Storage {
			out.Storage = append(out.Storage, slot{key, value})
		}
		sort.Slice(out.Storage, func(i, j int) bool { return bytes.Compare(out.Storage[i].Key[:], out.Storage[j].Key[:]) < 0 })
		p.GenesisAccounts = append(p.GenesisAccounts, out)
	}
	sort.Slice(p.GenesisAccounts, func(i, j int) bool {
		return bytes.Compare(p.GenesisAccounts[i].Address[:], p.GenesisAccounts[j].Address[:]) < 0
	})
	model := initial(g)
	firstTime := cfg.L2GenesisTimestamp + 1
	for _, number := range []uint64{1, 2, 8191, 8192, 8193, 16383} {
		m := &znn.Momentum{Version: 1, ChainIdentifier: cfg.L1ChainID, Height: cfg.L1StartHeight + number - 1,
			Timestamp: cfg.L2GenesisTimestamp + number, Hash: znn.Sha3([]byte(fmt.Sprintf("offline-system-predeploy/%s/%d", name, number)))}
		plan, err := derive.BuildPlan(cfg, &znn.DetailedMomentum{Momentum: m}, nil)
		must(err)
		attrs, err := plan.Attributes()
		must(err)
		check(attrs.BeaconRoot != nil && *attrs.BeaconRoot == common.Hash(m.Hash) && attrs.Timestamp == m.Timestamp && *attrs.GasLimit == cfg.GasLimit)
		parent := common.Hash(znn.Sha3([]byte(fmt.Sprintf("offline-parent/%s/%d", name, number))))
		if number == 1 {
			parent = p.GenesisHash
		}
		fee := uint64(params.InitialBaseFee)
		if minimum := cfg.MinBaseFeeAt(m.Height); minimum > fee {
			fee = minimum
		}
		b := block{Number: number, Timestamp: attrs.Timestamp, GasLimit: *attrs.GasLimit, BaseFee: fee, BeaconRoot: *attrs.BeaconRoot, ParentHash: parent, Reads: []read{}}
		// Independent specification model: no EVM result is used to fill it.
		model.SetState(params.BeaconRootsAddress, word(b.Timestamp%window), word(b.Timestamp))
		model.SetState(params.BeaconRootsAddress, word(b.Timestamp%window+window), b.BeaconRoot)
		b.AfterBeaconRoot = model.IntermediateRoot(true)
		model.SetState(params.HistoryStorageAddress, word((b.Number-1)%window), b.ParentHash)
		b.AfterHistoryRoot = model.IntermediateRoot(true)
		currentTime, zero, earliest, previous, current := word(b.Timestamp), word(0), word(firstTime), word(b.Number-1), word(b.Number)
		addRead(&b, model, "beacon-current", params.BeaconRootsAddress, currentTime[:])
		addRead(&b, model, "beacon-zero", params.BeaconRootsAddress, zero[:])
		addRead(&b, model, "beacon-short", params.BeaconRootsAddress, currentTime[:31])
		addRead(&b, model, "beacon-oldest", params.BeaconRootsAddress, earliest[:])
		addRead(&b, model, "history-parent", params.HistoryStorageAddress, previous[:])
		addRead(&b, model, "history-current", params.HistoryStorageAddress, current[:])
		addRead(&b, model, "history-short", params.HistoryStorageAddress, previous[:31])
		addRead(&b, model, "history-genesis", params.HistoryStorageAddress, zero[:])
		untrusted := common.HexToHash("0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
		addRead(&b, model, "beacon-nonsystem-write", params.BeaconRootsAddress, untrusted[:])
		addRead(&b, model, "history-nonsystem-write", params.HistoryStorageAddress, untrusted[:])
		check(model.Error() == nil && len(b.Reads) == 10)
		p.Blocks = append(p.Blocks, b)
	}
	if !cfg.IsKarst() {
		reference(cfg, g, &p)
		check(len(p.GoRoots) == 12)
	}
	return p
}
func main() {
	check(len(os.Args) == 2)
	profiles := []profile{}
	for _, name := range []string{"supported-isthmus", "l2", "l2-69", "l2-dp"} {
		p := build(name)
		profiles = append(profiles, p)
		fmt.Printf("SYSTEM-PREDEPLOY-GO profile=%s contexts=6 specificationRoots=12 getterChecks=60 goReferenceExecuted=%t genesisAccounts=12\n", name, p.GoReferenceExecuted)
	}
	raw, err := json.Marshal(struct {
		Schema   int       `json:"schema"`
		Scope    string    `json:"scope"`
		Profiles []profile `json:"profiles"`
	}{1, "source-bound synthetic block contexts, production system hooks and independent specification roots; no connected client chain, fees or release acceptance", profiles})
	must(err)
	check(len(raw) > 0 && len(raw) <= 4<<20)
	f, err := os.OpenFile(os.Args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	must(err)
	_, err = f.Write(raw)
	must(err)
	must(f.Close())
	fmt.Printf("SYSTEM-PREDEPLOY-INPUT sha256=%s bytes=%d profiles=4 nativeExecutionPerformed=false\n", digest(raw), len(raw))
}
