package l2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"

	"github.com/Zenoglyphs-VM/zvm-core/rollup"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// This opt-in recipe describes two canonical CREATE2 constructor frames from
// the existing nonce-one Arachnid factory, with the three target accounts
// initially absent. It is a genesis-state recipe, not a deployment transaction
// history, proof of fees/finality, or authority to change an existing chain.
func installPredeployState(cfg *rollup.Config, alloc types.GenesisAlloc) error {
	if cfg.PredeployState == rollup.PredeployStateLegacy {
		return nil
	}
	if cfg.PredeployState != rollup.PredeployStateConstructorsV1 || cfg.L2ChainID == 0 {
		return fmt.Errorf("unsupported constructor-state genesis context")
	}
	addresses := Predeploys()
	// These full-byte fingerprints bind version one to the reviewed material.
	// A different source/compiler/runtime needs another reviewed recipe.
	pins := map[string]string{
		"Permit2":                  "5b4c4aeff7672fe4493c30120610318a79c1c3129e8d0ecc7861d4867c481a3b",
		"EntryPoint_v07":           "674d73ee2acf6d72a5b32b6e05150b9ddcfcee0429a0e7914929faccbc2bf4d5",
		"SenderCreator_v07":        "46ce0a80a81a08523313640038083c3941ebb67909ec13b6ba257b0d3dc4a1db",
		"Create2Deployer_Arachnid": "e0af82ad2e5188285db8ba0b2ae054d13f0fd4fe325c48c7f2dace48454404b9",
	}
	for name, pin := range pins {
		account, ok := alloc[addresses[name]]
		digest := sha256.Sum256(account.Code)
		if !ok || account.Nonce != 1 || account.Balance == nil || account.Balance.Sign() != 0 || len(account.Storage) != 0 || hex.EncodeToString(digest[:]) != pin {
			return fmt.Errorf("constructor-state source account %s differs", name)
		}
	}
	entry, child := addresses["EntryPoint_v07"], addresses["SenderCreator_v07"]
	if crypto.CreateAddress(entry, 1) != child {
		return fmt.Errorf("constructor-state child address differs")
	}
	permit := alloc[addresses["Permit2"]]
	permit.Code = bytes.Clone(permit.Code)
	// The offsets are the complete two immutable ranges of pinned solc output
	// 1032 (chain ID) and 1030 (domain). They are not caller-provided masks.
	const chainOffset, domainOffset = 6945, 6983
	if len(permit.Code) != 9152 || !bytes.Equal(permit.Code[chainOffset:chainOffset+32], common.LeftPadBytes(big.NewInt(10).Bytes(), 32)) || !bytes.Equal(permit.Code[domainOffset:domainOffset+32], permitDomain(10, addresses["Permit2"])) {
		return fmt.Errorf("constructor-state historical Permit2 cache differs")
	}
	template := bytes.Clone(permit.Code)
	clear(template[chainOffset : chainOffset+32])
	clear(template[domainOffset : domainOffset+32])
	hash := sha256.Sum256(template)
	if hex.EncodeToString(hash[:]) != "0138d7a3a8d11b824ac3a7bf309c4f44a42f4e1ef2484451f0c3f4db6a03a298" {
		return fmt.Errorf("constructor-state Permit2 template differs")
	}
	copy(permit.Code[chainOffset:chainOffset+32], common.LeftPadBytes(new(big.Int).SetUint64(cfg.L2ChainID).Bytes(), 32))
	copy(permit.Code[domainOffset:domainOffset+32], permitDomain(cfg.L2ChainID, addresses["Permit2"]))
	alloc[addresses["Permit2"]] = permit
	parent := alloc[entry]
	parent.Nonce = 2
	parent.Storage = map[common.Hash]common.Hash{common.BigToHash(big.NewInt(2)): common.BigToHash(big.NewInt(1))}
	alloc[entry] = parent
	factory := alloc[addresses["Create2Deployer_Arachnid"]]
	factory.Nonce = 3 // One successful CREATE2 per Permit2 and EntryPoint frame.
	alloc[addresses["Create2Deployer_Arachnid"]] = factory
	return nil
}

func permitDomain(chain uint64, address common.Address) []byte {
	encoded := append(crypto.Keccak256([]byte("EIP712Domain(string name,uint256 chainId,address verifyingContract)")), crypto.Keccak256([]byte("Permit2"))...)
	encoded = append(encoded, common.LeftPadBytes(new(big.Int).SetUint64(chain).Bytes(), 32)...)
	encoded = append(encoded, common.LeftPadBytes(address.Bytes(), 32)...)
	return crypto.Keccak256(encoded)
}
