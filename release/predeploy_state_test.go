package release

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Zenoglyphs-VM/zvm-core/l2"
	"github.com/Zenoglyphs-VM/zvm-core/rollup"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
)

// These signatures belong only to newFixture's synthetic policy. Selecting a
// recipe is not an authorization to change a deployed chain or sign a release.
func repinPredeployState(t *testing.T, f *fixture, cfg *rollup.Config, g *core.Genesis) {
	t.Helper()
	cfg.L2GenesisHash = g.ToBlock().Hash()
	config, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	genesis, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	for i, pin := range f.manifest.Files {
		var raw []byte
		switch pin.Kind {
		case "rollup":
			raw = config
		case "l2-genesis":
			raw = genesis
		default:
			continue
		}
		pin.Bytes, pin.SHA256 = int64(len(raw)), digest(raw)
		f.manifest.Files[i] = pin
		if err := os.WriteFile(filepath.Join(f.files, pin.Repository, pin.Path), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	f.manifest.Epoch.L1ChainID = strconv.FormatUint(cfg.L1ChainID, 10)
	f.manifest.Epoch.L2ChainID = strconv.FormatUint(cfg.L2ChainID, 10)
	f.manifest.Epoch.L2GenesisHash = strings.ToLower(cfg.L2GenesisHash.Hex())
	f.manifest.Epoch.RollupSHA256 = digest(config)
	f.manifest.Epoch.EVM = cfg.EVM
}

func constructorAllocationFixture(t *testing.T, profile string) (*fixture, *rollup.Config, *core.Genesis) {
	t.Helper()
	f, _ := allocationFixture(t, profile, true)
	var cfg *rollup.Config
	for _, pin := range f.manifest.Files {
		if pin.Kind != "rollup" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(f.files, pin.Repository, pin.Path))
		if err != nil {
			t.Fatal(err)
		}
		cfg, err = rollup.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
	}
	if cfg == nil {
		t.Fatal("rollup pin missing")
	}
	cfg.PredeployState = rollup.PredeployStateConstructorsV1
	g, err := l2.BuildGenesis(cfg, cfg.L2GenesisTimestamp)
	if err != nil {
		t.Fatal(err)
	}
	repinPredeployState(t, f, cfg, g)
	return f, cfg, g
}

func TestSignedConstructorStateRecipe(t *testing.T) {
	for _, profile := range []string{"supported-isthmus", "l2", "l2-69", "l2-dp"} {
		t.Run(profile, func(t *testing.T) {
			f, _, g := constructorAllocationFixture(t, profile)
			result, err := f.manifest.Verify(context.Background(), f.policy, f.signatures(t), f.keys, f.files, f.sources)
			if err != nil {
				t.Fatal(err)
			}
			receipt := result.GenesisAllocation
			if receipt.PredeployState != string(rollup.PredeployStateConstructorsV1) || receipt.StateRoot != g.ToBlock().Root().Hex() || receipt.BlockHash != g.ToBlock().Hash().Hex() || len(receipt.Predeploys) != 12 {
				t.Fatal("selected receipt lost its full state binding")
			}
			if result.TrustedSignatures != 2 || result.VerifiedFiles != 7 || result.RuntimeObserved || result.CompilerExecuted || receipt.ConstructorExecutionObserved {
				t.Fatal("offline receipt invented execution or owner authority")
			}
			for _, a := range receipt.Predeploys {
				state := g.Alloc[l2.Predeploys()[a.Name]]
				if a.Nonce != strconv.FormatUint(state.Nonce, 10) || a.Balance != "0" || a.RuntimeCodeHash != crypto.Keccak256Hash(state.Code).Hex() {
					t.Fatal("constructor-state account differs")
				}
				switch a.Name {
				case "EntryPoint_v07":
					want := []GenesisSlot{{Slot: common.BigToHash(big.NewInt(2)).Hex(), Value: common.BigToHash(big.NewInt(1)).Hex()}}
					if a.Nonce != "2" || !reflect.DeepEqual(a.Storage, want) {
						t.Fatal("EntryPoint nonce/status omitted")
					}
				case "Create2Deployer_Arachnid":
					if a.Nonce != "3" {
						t.Fatal("factory constructor frame nonce omitted")
					}
				}
			}
			t.Logf("CONSTRUCTOR-STATE-SIGNED profile=%s accounts=12 fixtureSignatures=2 selectedRecipe=source-constructors-v1 constructorExecutionObserved=false", profile)
		})
	}
}

func TestSignedConstructorStateRefusesRepinnedDifferences(t *testing.T) {
	for _, name := range []string{"entry_nonce", "entry_status", "child_code", "factory_nonce", "permit_cache", "legacy_state"} {
		t.Run(name, func(t *testing.T) {
			f, cfg, g := constructorAllocationFixture(t, "supported-isthmus")
			addresses := l2.Predeploys()
			switch name {
			case "entry_nonce":
				a := g.Alloc[addresses["EntryPoint_v07"]]
				a.Nonce = 1
				g.Alloc[addresses["EntryPoint_v07"]] = a
			case "entry_status":
				delete(g.Alloc[addresses["EntryPoint_v07"]].Storage, common.BigToHash(big.NewInt(2)))
			case "child_code":
				g.Alloc[addresses["SenderCreator_v07"]].Code[0] ^= 1
			case "factory_nonce":
				a := g.Alloc[addresses["Create2Deployer_Arachnid"]]
				a.Nonce = 1
				g.Alloc[addresses["Create2Deployer_Arachnid"]] = a
			case "permit_cache":
				code, err := l2.PredeployCode("Permit2")
				if err != nil {
					t.Fatal(err)
				}
				a := g.Alloc[addresses["Permit2"]]
				a.Code = code
				g.Alloc[addresses["Permit2"]] = a
			case "legacy_state":
				old := *cfg
				old.PredeployState = rollup.PredeployStateLegacy
				var err error
				g, err = l2.BuildGenesis(&old, old.L2GenesisTimestamp)
				if err != nil {
					t.Fatal(err)
				}
			}
			// Repin every changed byte and the declared epoch hash, then sign again.
			// Refusal must come from the supported construction, not a stale file pin.
			repinPredeployState(t, f, cfg, g)
			result, err := f.manifest.Verify(context.Background(), f.policy, f.signatures(t), f.keys, f.files, f.sources)
			if err == nil || !reflect.DeepEqual(result, Receipt{}) {
				t.Fatal("repinned unsupported recipe returned a trusted receipt")
			}
			t.Logf("CONSTRUCTOR-STATE-SIGNED-REFUSAL case=%s freshSyntheticSignatures=true emptyReceipt=true", name)
		})
	}
}
