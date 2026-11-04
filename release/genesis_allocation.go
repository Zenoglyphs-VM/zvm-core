package release

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/Zenoglyphs-VM/zvm-core/l2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/crypto"
)

// GenesisAllocation records the supported allocation checked by Verify. It is
// an offline state description, not a receipt for constructor execution.
type GenesisAllocation struct {
	BlockHash                    string             `json:"blockHash"`
	StateRoot                    string             `json:"stateRoot"`
	Installation                 string             `json:"installation"`
	PredeployState               string             `json:"predeployState,omitempty"`
	ConstructorExecutionObserved bool               `json:"constructorExecutionObserved"`
	Predeploys                   []GenesisPredeploy `json:"predeploys"`
}

type GenesisPredeploy struct {
	Name            string        `json:"name"`
	Address         string        `json:"address"`
	Nonce           string        `json:"nonce"`
	Balance         string        `json:"balance"`
	RuntimeCodeHash string        `json:"runtimeCodeHash"`
	Storage         []GenesisSlot `json:"storage"`
}

type GenesisSlot struct {
	Slot  string `json:"slot"`
	Value string `json:"value"`
}

// Call only after VerifyEpochFiles and all other verification predicates. The
// second bounded read checks the same signed byte pin, so a replacement cannot
// make the receipt describe different bytes from the verified genesis.
func (m Manifest) genesisAllocation(directory string) (GenesisAllocation, error) {
	var result GenesisAllocation
	root, err := openDirectory(directory)
	if err != nil {
		return result, errors.New("genesis allocation root could not be opened")
	}
	defer root.Close()
	var raw, rollupRaw []byte
	for _, pin := range m.Files {
		switch pin.Kind {
		case "l2-genesis":
			raw, err = readPin(root, pin, 16<<20)
		case "rollup":
			rollupRaw, err = readPin(root, pin, MaxDocumentBytes)
		}
		if err != nil {
			return result, err
		}
	}
	cfg, err := m.BindConfiguration(rollupRaw)
	if err != nil {
		return result, err
	}
	var g core.Genesis
	if len(raw) == 0 || json.Unmarshal(raw, &g) != nil {
		return result, errors.New("verified genesis allocation could not be decoded")
	}
	blockHash, err := genesisHash(&g)
	if err != nil || strings.ToLower(blockHash.Hex()) != m.Epoch.L2GenesisHash {
		return result, errors.New("genesis allocation disagrees with the verified epoch")
	}
	addresses := l2.Predeploys()
	names := make([]string, 0, len(addresses))
	for name := range addresses {
		names = append(names, name)
	}
	slices.Sort(names)
	accounts := make([]GenesisPredeploy, 0, len(names))
	for _, name := range names {
		address := addresses[name]
		account, ok := g.Alloc[address]
		if !ok || account.Balance == nil || len(account.Code) == 0 {
			return result, errors.New("verified genesis omitted a supported predeploy account")
		}
		slots := make([]string, 0, len(account.Storage))
		values := map[string]string{}
		for slot, value := range account.Storage {
			if value != (common.Hash{}) {
				key := slot.Hex()
				slots = append(slots, key)
				values[key] = value.Hex()
			}
		}
		slices.Sort(slots)
		storage := make([]GenesisSlot, 0, len(slots))
		for _, slot := range slots {
			storage = append(storage, GenesisSlot{Slot: slot, Value: values[slot]})
		}
		accounts = append(accounts, GenesisPredeploy{Name: name, Address: strings.ToLower(address.Hex()),
			Nonce: strconv.FormatUint(account.Nonce, 10), Balance: account.Balance.String(),
			RuntimeCodeHash: crypto.Keccak256Hash(account.Code).Hex(), Storage: storage})
	}
	return GenesisAllocation{BlockHash: blockHash.Hex(), StateRoot: g.ToBlock().Root().Hex(),
		PredeployState: string(cfg.PredeployState),
		Installation:   "supported-genesis-allocation", ConstructorExecutionObserved: false, Predeploys: accounts}, nil
}
