// Command predeploy-installation-fixtures predicts canonical constructor-state
// recipes independently of the genesis builder and executes only the supported
// Isthmus reference frames. The native OP checker covers all five profiles.
package main

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"sort"

	"github.com/Zenoglyphs-VM/zvm-core/l2"
	"github.com/Zenoglyphs-VM/zvm-core/rollup"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// Complete original compiler output, including terminal Solidity metadata.
//
//go:embed testdata/canonical-material.json
var materialRaw []byte

const materialSHA = "c6d8935aa594ac0932d6b8728fa53193d0c47f33690ef217310bdf7be698ff76"
const permitSalt = "0x0000000000000000000000000000000000000000d3af2663da51c10215000000"
const entrySalt = "0x90d8084deab30c2a37c45e8d47f49f2f7965183cb6990a98943ef94940681de3"

type target struct {
	Address     common.Address                           `json:"address"`
	Creation    string                                   `json:"creation"`
	CreationSHA string                                   `json:"creationSha256"`
	Runtime     string                                   `json:"runtimeTemplate"`
	RuntimeSHA  string                                   `json:"runtimeTemplateSha256"`
	References  map[string][]struct{ Start, Length int } `json:"immutableReferences"`
}
type material struct {
	Salt    string            `json:"salt"`
	Factory common.Address    `json:"factory"`
	Targets map[string]target `json:"targets"`
}
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
type step struct {
	Name         string         `json:"name"`
	Salt         common.Hash    `json:"salt"`
	Creation     hexutil.Bytes  `json:"creation"`
	CreationSHA  string         `json:"creationSha256"`
	Address      common.Address `json:"address"`
	Runtime      hexutil.Bytes  `json:"runtime"`
	ChildRuntime hexutil.Bytes  `json:"childRuntime"`
	AfterRoot    common.Hash    `json:"afterRoot"`
}
type read struct {
	ID      string         `json:"id"`
	Address common.Address `json:"address"`
	Data    hexutil.Bytes  `json:"data"`
	Output  hexutil.Bytes  `json:"output"`
}
type profile struct {
	Name                 string        `json:"name"`
	Rules                string        `json:"rules"`
	ChainID              uint64        `json:"chainId"`
	Timestamp            uint64        `json:"timestamp"`
	GasLimit             uint64        `json:"gasLimit"`
	BaseFee              uint64        `json:"baseFee"`
	PredeployState       string        `json:"predeployState"`
	RollupSHA            string        `json:"rollupSha256"`
	MaterialSHA          string        `json:"materialSha256"`
	LegacyGenesisHash    common.Hash   `json:"legacyGenesisHash"`
	LegacyStateRoot      common.Hash   `json:"legacyStateRoot"`
	CandidateGenesisHash common.Hash   `json:"candidateGenesisHash"`
	CandidateStateRoot   common.Hash   `json:"candidateStateRoot"`
	GenesisAccounts      []account     `json:"genesisAccounts"`
	BeforeRoot           common.Hash   `json:"beforeRoot"`
	Steps                []step        `json:"steps"`
	Reads                []read        `json:"reads"`
	GoReferenceExecuted  bool          `json:"goReferenceExecuted"`
	GoRoots              []common.Hash `json:"goRoots"`
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
func need(ok bool) {
	if !ok {
		panic("source-bound constructor-state recipe differs")
	}
}
func digest(raw []byte) string  { value := sha256.Sum256(raw); return hex.EncodeToString(value[:]) }
func word(n uint64) common.Hash { return common.BigToHash(new(big.Int).SetUint64(n)) }
func decode(raw string) []byte  { out, err := hex.DecodeString(raw); must(err); return out }
func domain(chain uint64, address common.Address) []byte {
	data := append(crypto.Keccak256([]byte("EIP712Domain(string name,uint256 chainId,address verifyingContract)")), crypto.Keccak256([]byte("Permit2"))...)
	w := word(chain)
	data = append(data, w[:]...)
	data = append(data, common.LeftPadBytes(address.Bytes(), 32)...)
	return crypto.Keccak256(data)
}
func installTemplate(t target, values map[string][]byte) []byte {
	raw := decode(t.Runtime)
	need(digest(raw) == t.RuntimeSHA && len(values) == len(t.References))
	used := make(map[int]bool)
	for id, ranges := range t.References {
		value, ok := values[id]
		need(ok && len(value) == 32 && len(ranges) > 0)
		for _, r := range ranges {
			need(r.Length == 32 && r.Start >= 0 && r.Start+32 <= len(raw))
			for offset := r.Start; offset < r.Start+32; offset++ {
				need(!used[offset] && raw[offset] == 0)
				used[offset] = true
			}
			copy(raw[r.Start:r.Start+32], value)
		}
	}
	return raw
}
func initial(alloc types.GenesisAlloc) *state.StateDB {
	st, err := state.New(types.EmptyRootHash, state.NewDatabaseForTesting())
	must(err)
	for address, a := range alloc {
		st.SetBalance(address, uint256.MustFromBig(a.Balance), tracing.BalanceIncreaseGenesisBalance)
		st.SetNonce(address, a.Nonce, tracing.NonceChangeGenesis)
		st.SetCode(address, a.Code, tracing.CodeChangeGenesis)
		for key, value := range a.Storage {
			st.SetState(address, key, value)
		}
	}
	need(st.Error() == nil)
	return st
}
func accounts(alloc types.GenesisAlloc) []account {
	out := []account{}
	for address, a := range alloc {
		need(a.Nonce == 1 && a.Balance != nil && a.Balance.Sign() == 0)
		row := account{address, a.Nonce, "0", bytes.Clone(a.Code), []slot{}}
		for key, value := range a.Storage {
			if value != (common.Hash{}) {
				row.Storage = append(row.Storage, slot{key, value})
			}
		}
		sort.Slice(row.Storage, func(i, j int) bool { return bytes.Compare(row.Storage[i].Key[:], row.Storage[j].Key[:]) < 0 })
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i].Address[:], out[j].Address[:]) < 0 })
	return out
}
func removeTargets(alloc types.GenesisAlloc) types.GenesisAlloc {
	out := types.GenesisAlloc{}
	addresses := l2.Predeploys()
	for address, a := range alloc {
		if address != addresses["Permit2"] && address != addresses["EntryPoint_v07"] && address != addresses["SenderCreator_v07"] {
			out[address] = a
		}
	}
	need(len(out) == 9)
	return out
}
func reference(cfg *rollup.Config, legacy *core.Genesis, p *profile) {
	need(!cfg.IsKarst())
	st := initial(removeTargets(legacy.Alloc))
	need(st.IntermediateRoot(true) == p.BeforeRoot)
	caller := common.HexToAddress("0x1234")
	factory := l2.Predeploys()["Create2Deployer_Arachnid"]
	random := crypto.Keccak256Hash([]byte("canonical-constructor-state/offline"))
	call := func(target common.Address, data, expected []byte, gas uint64) {
		e := vm.NewEVM(vm.BlockContext{CanTransfer: core.CanTransfer, Transfer: core.Transfer, GetHash: func(uint64) common.Hash { panic("constructor recipe must not request BLOCKHASH") },
			BlockNumber: big.NewInt(1), Time: p.Timestamp, GasLimit: p.GasLimit, BaseFee: new(big.Int).SetUint64(p.BaseFee), BlobBaseFee: big.NewInt(1), Difficulty: new(big.Int), Random: &random}, st, l2.ChainConfig(cfg), vm.Config{})
		st.Prepare(l2.ChainConfig(cfg).Rules(big.NewInt(1), true, p.Timestamp), caller, common.Address{}, &target, nil, nil)
		e.SetTxContext(vm.TxContext{Origin: caller, GasPrice: new(uint256.Int)})
		output, _, err := e.Call(caller, target, data, gas, common.U2560)
		must(err)
		need(bytes.Equal(output, expected))
		st.Finalise(true)
		need(st.Error() == nil && st.GetNonce(caller) == 0 && st.GetBalance(caller).IsZero())
	}
	for _, s := range p.Steps {
		call(factory, append(bytes.Clone(s.Salt[:]), s.Creation...), s.Address.Bytes(), 8_000_000)
		root := st.IntermediateRoot(true)
		need(root == s.AfterRoot)
		p.GoRoots = append(p.GoRoots, root)
	}
	need(st.IntermediateRoot(true) == p.CandidateStateRoot)
	for _, r := range p.Reads {
		before := st.IntermediateRoot(true)
		call(r.Address, r.Data, r.Output, 200_000)
		need(st.IntermediateRoot(true) == before)
	}
	p.GoReferenceExecuted = true
}

func build(name string, materials map[string]material) profile {
	path := name
	if name == "supported-isthmus" || name == "historical-chain-10" {
		path = "l2"
	}
	raw, err := os.ReadFile("chains/" + path + "/rollup.json")
	must(err)
	cfg, err := rollup.Parse(raw)
	must(err)
	if name == "supported-isthmus" || name == "historical-chain-10" {
		cfg.EVM = rollup.EVMIsthmus
	}
	if name == "historical-chain-10" {
		cfg.L2ChainID = 10
	}
	legacy, err := l2.BuildGenesis(cfg, cfg.L2GenesisTimestamp)
	must(err)
	if name != "supported-isthmus" && name != "historical-chain-10" {
		need(legacy.ToBlock().Hash() == cfg.L2GenesisHash)
	}
	cfg.PredeployState = rollup.PredeployStateConstructorsV1
	candidate, err := l2.BuildGenesis(cfg, cfg.L2GenesisTimestamp)
	must(err)
	cfg.L2GenesisHash = candidate.ToBlock().Hash()
	raw, err = json.Marshal(cfg)
	must(err)
	p := profile{Name: name, Rules: cfg.EVM, ChainID: cfg.L2ChainID, Timestamp: cfg.L2GenesisTimestamp + 1, GasLimit: cfg.GasLimit, BaseFee: params.InitialBaseFee,
		PredeployState: string(cfg.PredeployState), RollupSHA: digest(raw), MaterialSHA: materialSHA, LegacyGenesisHash: legacy.ToBlock().Hash(), LegacyStateRoot: legacy.ToBlock().Root(), CandidateGenesisHash: candidate.ToBlock().Hash(), CandidateStateRoot: candidate.ToBlock().Root(),
		GenesisAccounts: accounts(legacy.Alloc), Steps: []step{}, Reads: []read{}, GoRoots: []common.Hash{}}
	need(len(p.GenesisAccounts) == 12 && p.CandidateStateRoot != p.LegacyStateRoot)
	addresses := l2.Predeploys()
	factory := addresses["Create2Deployer_Arachnid"]
	child := addresses["SenderCreator_v07"]
	entry := addresses["EntryPoint_v07"]
	need(crypto.CreateAddress(entry, 1) == child)
	model := initial(removeTargets(legacy.Alloc))
	p.BeforeRoot = model.IntermediateRoot(true)
	for index, key := range []string{"permit2", "entrypoint"} {
		m := materials[key]
		name := "Permit2"
		salt := permitSalt
		creationPin := "061c1d230d740e10d9e7ff23c3bc1f918abd20c3d017188697ba205d61c666ff"
		values := map[string][]byte{}
		if index == 0 {
			w := word(cfg.L2ChainID)
			values["1032"] = w[:]
			values["1030"] = domain(cfg.L2ChainID, addresses[name])
		} else {
			name = "EntryPoint_v07"
			salt = entrySalt
			creationPin = "a227a5ba81844120bf91c337dca44002b26f51878f4a8acd9b7b3fb6a64ed054"
			values["140"] = common.LeftPadBytes(child.Bytes(), 32)
		}
		t := m.Targets[name]
		creation := decode(t.Creation)
		need(m.Factory == factory && m.Salt == salt && t.Address == addresses[name] && digest(creation) == creationPin && t.CreationSHA == creationPin)
		address := crypto.CreateAddress2(factory, common.HexToHash(salt), crypto.Keccak256(creation))
		need(address == t.Address)
		runtime := installTemplate(t, values)
		s := step{Name: name, Salt: common.HexToHash(salt), Creation: creation, CreationSHA: creationPin, Address: address, Runtime: runtime, ChildRuntime: []byte{}}
		model.SetBalance(address, new(uint256.Int), tracing.BalanceIncreaseGenesisBalance)
		model.SetNonce(address, 1, tracing.NonceChangeGenesis)
		model.SetCode(address, runtime, tracing.CodeChangeGenesis)
		model.SetNonce(factory, uint64(index+2), tracing.NonceChangeGenesis)
		if index == 1 {
			model.SetNonce(address, 2, tracing.NonceChangeGenesis)
			model.SetState(address, word(2), word(1))
			ct := m.Targets["SenderCreator_v07"]
			s.ChildRuntime = installTemplate(ct, map[string][]byte{})
			need(ct.Address == child && digest(s.ChildRuntime) == "46ce0a80a81a08523313640038083c3941ebb67909ec13b6ba257b0d3dc4a1db")
			model.SetBalance(child, new(uint256.Int), tracing.BalanceIncreaseGenesisBalance)
			model.SetNonce(child, 1, tracing.NonceChangeGenesis)
			model.SetCode(child, s.ChildRuntime, tracing.CodeChangeGenesis)
		}
		s.AfterRoot = model.IntermediateRoot(true)
		p.Steps = append(p.Steps, s)
	}
	need(model.Error() == nil && model.IntermediateRoot(true) == p.CandidateStateRoot)
	reader := common.HexToAddress("0x1234")
	addRead := func(id string, address common.Address, signature string, args, out []byte) {
		data := append(crypto.Keccak256([]byte(signature))[:4], args...)
		p.Reads = append(p.Reads, read{id, address, data, bytes.Clone(out)})
	}
	addRead("current-chain-domain", addresses["Permit2"], "DOMAIN_SEPARATOR()", nil, domain(cfg.L2ChainID, addresses["Permit2"]))
	for i, address := range []common.Address{reader, common.Address{}} {
		addRead(fmt.Sprintf("empty-deposit-%d", i), entry, "balanceOf(address)", common.LeftPadBytes(address.Bytes(), 32), make([]byte, 32))
	}
	for _, key := range []uint64{0, 1} {
		w := word(key)
		addRead(fmt.Sprintf("empty-nonce-%d", key), entry, "getNonce(address,uint192)", append(common.LeftPadBytes(reader.Bytes(), 32), w[:]...), new(big.Int).Lsh(new(big.Int).SetUint64(key), 64).FillBytes(make([]byte, 32)))
	}
	if !cfg.IsKarst() {
		reference(cfg, legacy, &p)
	}
	fmt.Printf("CONSTRUCTOR-STATE-MODEL profile=%s rules=%s chainId=%d canonicalFrames=2 independentModelRoots=2 accounts=12 goReferenceExecuted=%t legacyGenesisHash=%s candidateGenesisHash=%s candidateStateRoot=%s\n", p.Name, p.Rules, p.ChainID, p.GoReferenceExecuted, p.LegacyGenesisHash, p.CandidateGenesisHash, p.CandidateStateRoot)
	return p
}

func main() {
	need(len(os.Args) == 2 && len(materialRaw) == 117727 && digest(materialRaw) == materialSHA)
	var input struct {
		Schema    int                 `json:"schema"`
		Materials map[string]material `json:"materials"`
	}
	must(json.Unmarshal(materialRaw, &input))
	need(input.Schema == 1 && len(input.Materials) == 2)
	profiles := []profile{}
	for _, name := range []string{"supported-isthmus", "l2", "l2-69", "l2-dp", "historical-chain-10"} {
		profiles = append(profiles, build(name, input.Materials))
	}
	output := struct {
		Schema   int       `json:"schema"`
		Scope    string    `json:"scope"`
		Profiles []profile `json:"profiles"`
	}{1, "Canonical CREATE2 direct constructor frames; source-predicted complete state, no signed transactions, chain activation or release authority.", profiles}
	raw, err := json.Marshal(output)
	must(err)
	need(len(raw) > 0 && len(raw) <= 4<<20)
	file, err := os.OpenFile(os.Args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	must(err)
	_, err = file.Write(raw)
	must(err)
	must(file.Close())
	fmt.Printf("CONSTRUCTOR-STATE-INPUT sha256=%s bytes=%d profiles=5 canonicalFrames=10 independentGoProfiles=2 originalChainFilesChanged=false\n", digest(raw), len(raw))
}
