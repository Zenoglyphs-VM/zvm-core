package release

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Zenoglyphs-VM/zvm-core/l2"
	"github.com/Zenoglyphs-VM/zvm-core/rollup"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
)

func allocationFixture(t *testing.T, profile string, fee bool) (*fixture, *core.Genesis) {
	t.Helper()
	f := newFixture(t)
	chain := profile
	if profile == "supported-isthmus" {
		chain = "l2"
	}
	raw, err := os.ReadFile("../chains/" + chain + "/rollup.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := rollup.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if profile == "supported-isthmus" {
		cfg.EVM = "isthmus"
	}
	if fee {
		cfg.BridgeFee = &rollup.BridgeFee{BPS: 75, MinZNN: 9, MinQSR: 13}
	}
	g, err := l2.BuildGenesis(cfg, cfg.L2GenesisTimestamp)
	if err != nil {
		t.Fatal(err)
	}
	cfg.L2GenesisHash = g.ToBlock().Hash()
	rollupRaw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	genesisRaw, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	for i, pin := range f.manifest.Files {
		var data []byte
		switch pin.Kind {
		case "rollup":
			data = rollupRaw
		case "l2-genesis":
			data = genesisRaw
		default:
			continue
		}
		pin.Bytes, pin.SHA256 = int64(len(data)), digest(data)
		f.manifest.Files[i] = pin
		if err := os.WriteFile(filepath.Join(f.files, pin.Repository, pin.Path), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	f.manifest.Epoch.L1ChainID = strconv.FormatUint(cfg.L1ChainID, 10)
	f.manifest.Epoch.L2ChainID = strconv.FormatUint(cfg.L2ChainID, 10)
	f.manifest.Epoch.L2GenesisHash = strings.ToLower(cfg.L2GenesisHash.Hex())
	f.manifest.Epoch.RollupSHA256 = digest(rollupRaw)
	f.manifest.Epoch.EVM = cfg.EVM
	return f, g
}

func TestSignedGenesisAllocationReceipt(t *testing.T) {
	for _, profile := range []string{"supported-isthmus", "l2", "l2-69", "l2-dp"} {
		t.Run(profile, func(t *testing.T) {
			f, g := allocationFixture(t, profile, false)
			r, err := f.manifest.Verify(context.Background(), f.policy, f.signatures(t), f.keys, f.files, f.sources)
			if err != nil {
				t.Fatal(err)
			}
			a := r.GenesisAllocation
			if r.TrustedSignatures != 2 || r.VerifiedFiles != 7 || r.RuntimeObserved || r.CompilerExecuted || a.ConstructorExecutionObserved || a.Installation != "supported-genesis-allocation" {
				t.Fatal("allocation receipt upgraded its offline scope")
			}
			if a.BlockHash != g.ToBlock().Hash().Hex() || a.StateRoot != g.ToBlock().Root().Hex() || len(a.Predeploys) != 12 {
				t.Fatal("allocation receipt lost its complete genesis binding")
			}
			names := []string{}
			for _, account := range a.Predeploys {
				names = append(names, account.Name)
				address, ok := l2.Predeploys()[account.Name]
				code, err := l2.PredeployCode(account.Name)
				if !ok || err != nil || account.Address != strings.ToLower(address.Hex()) || account.RuntimeCodeHash != crypto.Keccak256Hash(code).Hex() || account.Nonce != "1" || account.Balance != "0" || account.Storage == nil {
					t.Fatal("receipt differs from the actual preinstalled account")
				}
				wanted := []GenesisSlot{}
				if account.Name == "ZVMBridge" {
					for slot, value := range map[int64]int64{10: 25, 11: 1000000, 12: 10000000} {
						wanted = append(wanted, GenesisSlot{Slot: common.BigToHash(big.NewInt(slot)).Hex(), Value: common.BigToHash(big.NewInt(value)).Hex()})
					}
					slices.SortFunc(wanted, func(a, b GenesisSlot) int { return strings.Compare(a.Slot, b.Slot) })
				}
				if !reflect.DeepEqual(account.Storage, wanted) {
					t.Fatal("receipt differs from the shipped nonzero allocation storage")
				}
				// In particular, an empty EntryPoint allocation must not be
				// silently replaced with a predicted constructor status word.
				if account.Name == "EntryPoint_v07" && len(g.Alloc[address].Storage) != 0 {
					t.Fatal("fixture no longer describes the shipped preinstallation")
				}
			}
			if !slices.IsSorted(names) {
				t.Fatal("predeploy receipt order is nondeterministic")
			}
			first, err := json.Marshal(a)
			if err != nil || bytes.Contains(first, []byte(f.files)) || bytes.Contains(first, []byte(f.sources)) {
				t.Fatal("allocation receipt exposed a private path")
			}
			second, err := f.manifest.Verify(context.Background(), f.policy, f.signatures(t), f.keys, f.files, f.sources)
			if err != nil || !reflect.DeepEqual(a, second.GenesisAllocation) {
				t.Fatal("same signed allocation produced different receipt bytes")
			}
			t.Logf("GENESIS-ALLOCATION pass=true profile=%s accounts=12 fixtureSignatures=2 files=7 constructorExecutionObserved=false runtimeObserved=false", profile)
		})
	}
}

func TestSignedGenesisAllocationReportsConfiguredStorage(t *testing.T) {
	f, _ := allocationFixture(t, "supported-isthmus", true)
	r, err := f.manifest.Verify(context.Background(), f.policy, f.signatures(t), f.keys, f.files, f.sources)
	if err != nil {
		t.Fatal(err)
	}
	wanted := []GenesisSlot{}
	for slot, value := range map[int64]int64{10: 75, 11: 9, 12: 13} {
		wanted = append(wanted, GenesisSlot{Slot: common.BigToHash(big.NewInt(slot)).Hex(), Value: common.BigToHash(big.NewInt(value)).Hex()})
	}
	slices.SortFunc(wanted, func(a, b GenesisSlot) int { return strings.Compare(a.Slot, b.Slot) })
	for _, account := range r.GenesisAllocation.Predeploys {
		if account.Name == "ZVMBridge" {
			if !reflect.DeepEqual(account.Storage, wanted) {
				t.Fatal("receipt did not retain all configured nonzero bridge slots")
			}
			t.Log("GENESIS-ALLOCATION-STORAGE pass=true slots=3 sorted=true constructorExecutionObserved=false")
			return
		}
	}
	t.Fatal("bridge account omitted")
}

func TestFailedReleaseVerificationReturnsNoAllocationReceipt(t *testing.T) {
	for _, kind := range []string{"signature", "file", "source"} {
		t.Run(kind, func(t *testing.T) {
			f, _ := allocationFixture(t, "supported-isthmus", false)
			signatures := f.signatures(t)
			switch kind {
			case "signature":
				signatures.Signatures = nil
			case "file":
				pin := f.manifest.Files[0]
				if err := os.WriteFile(filepath.Join(f.files, pin.Repository, pin.Path), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "source":
				if err := os.WriteFile(filepath.Join(f.sources, "zvm-core", "README.md"), []byte("dirty source"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			r, err := f.manifest.Verify(context.Background(), f.policy, signatures, f.keys, f.files, f.sources)
			if err == nil || !reflect.DeepEqual(r, Receipt{}) {
				t.Fatal("failed verification returned a trusted allocation receipt")
			}
			t.Logf("GENESIS-ALLOCATION-REFUSAL pass=true case=%s emptyReceipt=true", kind)
		})
	}
}
