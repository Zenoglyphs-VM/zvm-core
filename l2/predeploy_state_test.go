package l2

import (
	"bytes"
	"math/big"
	"reflect"
	"testing"

	"github.com/Zenoglyphs-VM/zvm-core/rollup"
	"github.com/ethereum/go-ethereum/common"
)

func TestGenesisPredeployStateProfiles(t *testing.T) {
	for _, name := range []string{"l2", "l2-69", "l2-dp"} {
		t.Run(name, func(t *testing.T) {
			cfg, err := rollup.Load("../chains/" + name + "/rollup.json")
			if err != nil {
				t.Fatal(err)
			}
			legacy, err := BuildGenesis(cfg, cfg.L2GenesisTimestamp)
			if err != nil {
				t.Fatal(err)
			}
			if legacy.ToBlock().Hash() != cfg.L2GenesisHash || len(legacy.Alloc) != 12 {
				t.Fatal("original genesis changed")
			}
			before := *cfg
			chosen := *cfg
			chosen.PredeployState = rollup.PredeployStateConstructorsV1
			derived, err := BuildGenesis(&chosen, chosen.L2GenesisTimestamp)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*cfg, before) || cfg.PredeployState != "" || chosen.L2GenesisHash != cfg.L2GenesisHash {
				t.Fatal("selection rewrote the source configuration or accepted hash")
			}
			if derived.ToBlock().Hash() == cfg.L2GenesisHash || derived.ToBlock().Root() == legacy.ToBlock().Root() || len(derived.Alloc) != 12 {
				t.Fatal("explicit recipe did not change the complete genesis commitment")
			}
			addresses := Predeploys()
			entry, factory, permit := addresses["EntryPoint_v07"], addresses["Create2Deployer_Arachnid"], addresses["Permit2"]
			for address, original := range legacy.Alloc {
				actual := derived.Alloc[address]
				if actual.Balance.Sign() != 0 {
					t.Fatal("recipe allocated value")
				}
				switch address {
				case entry:
					want := map[common.Hash]common.Hash{common.BigToHash(big.NewInt(2)): common.BigToHash(big.NewInt(1))}
					if original.Nonce != 1 || len(original.Storage) != 0 || actual.Nonce != 2 || !reflect.DeepEqual(actual.Storage, want) || !bytes.Equal(actual.Code, original.Code) {
						t.Fatal("EntryPoint constructor state differs")
					}
				case factory:
					if original.Nonce != 1 || actual.Nonce != 3 || !bytes.Equal(actual.Code, original.Code) || !reflect.DeepEqual(actual.Storage, original.Storage) {
						t.Fatal("factory frame nonce differs")
					}
				case permit:
					if actual.Nonce != 1 || len(actual.Storage) != 0 || bytes.Equal(actual.Code, original.Code) {
						t.Fatal("Permit2 current-chain cache differs")
					}
				default:
					if !reflect.DeepEqual(actual, original) {
						t.Fatal("unrelated account changed", address)
					}
				}
			}
			originalPermit := bytes.Clone(legacy.Alloc[permit].Code)
			derived.Alloc[permit].Code[0] ^= 1
			if !bytes.Equal(legacy.Alloc[permit].Code, originalPermit) {
				t.Fatal("independent genesis code alias")
			}
			rebuilt, err := BuildGenesis(cfg, cfg.L2GenesisTimestamp)
			if err != nil || rebuilt.ToBlock().Hash() != cfg.L2GenesisHash {
				t.Fatal("candidate mutation changed later legacy construction")
			}
		})
	}
}

func TestGenesisPredeployStateRefusals(t *testing.T) {
	if _, err := BuildGenesis(nil, 0); err == nil {
		t.Fatal("nil configuration accepted")
	}
	for _, cfg := range []rollup.Config{{PredeployState: rollup.PredeployState("unknown")}, {PredeployState: rollup.PredeployStateConstructorsV1}} {
		if _, err := BuildGenesis(&cfg, 0); err == nil {
			t.Fatal("unsupported recipe context accepted")
		}
	}
}
