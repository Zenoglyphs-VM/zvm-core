package release

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/core"
)

// Duplicate one existing member without changing its value. Object traversal
// uses the original unambiguous fixture; the output deliberately has two keys.
func duplicateGenesisMember(t *testing.T, raw []byte, path []string, key, encodedKey string) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		t.Fatal("invalid genesis control object")
	}
	if len(path) != 0 {
		child, ok := object[path[0]]
		if !ok {
			t.Fatal("missing genesis control path")
		}
		object[path[0]] = duplicateGenesisMember(t, child, path[1:], key, encodedKey)
	} else if _, ok := object[key]; !ok {
		t.Fatal("missing genesis control member")
	}
	keys := make([]string, 0, len(object))
	for name := range object {
		keys = append(keys, name)
	}
	slices.Sort(keys)
	var out bytes.Buffer
	out.WriteByte('{')
	for index, name := range keys {
		if index != 0 {
			out.WriteByte(',')
		}
		quoted, _ := json.Marshal(name)
		out.Write(quoted)
		out.WriteByte(':')
		out.Write(object[name])
		if len(path) == 0 && name == key {
			out.WriteByte(',')
			if encodedKey != "" {
				out.WriteString(encodedKey)
			} else {
				out.Write(quoted)
			}
			out.WriteByte(':')
			out.Write(object[name])
		}
	}
	out.WriteByte('}')
	return out.Bytes()
}

func ignoredGenesisValue(t *testing.T, raw []byte, value []byte) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	// RawMessage validation would reject the intentionally invalid UTF-8 case
	// on some toolchains. Preserve its exact bytes instead of normalizing them.
	base, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return append(append(base[:len(base)-1], []byte(`,"releaseFixtureOnly":`)...), append(value, '}')...)
}

func TestReleaseGenesisJSONIsUnambiguous(t *testing.T) {
	baseline := newFixture(t)
	if _, err := baseline.manifest.Verify(context.Background(), baseline.policy, baseline.signatures(t), baseline.keys, baseline.files, baseline.sources); err != nil {
		t.Fatal(err)
	}
	controlNames := []string{
		"root_timestamp", "root_config", "root_alloc", "root_nonce",
		"config_chain_id", "config_optimism", "optimism_elasticity",
		"allocation_account", "account_code", "account_nonce", "account_balance",
		"escaped_root_timestamp", "ignored_nested_member", "invalid_utf8_ignored_value",
		"excessive_ignored_nesting", "excessive_ignored_structure",
	}
	for _, name := range controlNames {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			index := -1
			for i, pin := range f.manifest.Files {
				if pin.Kind == "l2-genesis" {
					index = i
				}
			}
			if index == -1 {
				t.Fatal("genesis pin missing")
			}
			pin := &f.manifest.Files[index]
			file := filepath.Join(f.files, pin.Repository, pin.Path)
			original, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var before core.Genesis
			if err := json.Unmarshal(original, &before); err != nil {
				t.Fatal(err)
			}
			var object map[string]json.RawMessage
			_ = json.Unmarshal(original, &object)
			var alloc map[string]json.RawMessage
			_ = json.Unmarshal(object["alloc"], &alloc)
			addresses := make([]string, 0, len(alloc))
			for address := range alloc {
				addresses = append(addresses, address)
			}
			slices.Sort(addresses)
			if len(addresses) == 0 {
				t.Fatal("empty genesis control inventory")
			}
			address := addresses[0]
			var changed []byte
			switch name {
			case "root_timestamp", "root_config", "root_alloc", "root_nonce":
				changed = duplicateGenesisMember(t, original, nil, strings.TrimPrefix(name, "root_"), "")
			case "config_chain_id":
				changed = duplicateGenesisMember(t, original, []string{"config"}, "chainId", "")
			case "config_optimism":
				changed = duplicateGenesisMember(t, original, []string{"config"}, "optimism", "")
			case "optimism_elasticity":
				changed = duplicateGenesisMember(t, original, []string{"config", "optimism"}, "eip1559Elasticity", "")
			case "allocation_account":
				changed = duplicateGenesisMember(t, original, []string{"alloc"}, address, "")
			case "account_code", "account_nonce", "account_balance":
				changed = duplicateGenesisMember(t, original, []string{"alloc", address}, strings.TrimPrefix(name, "account_"), "")
			case "escaped_root_timestamp":
				changed = duplicateGenesisMember(t, original, nil, "timestamp", `"\u0074imestamp"`)
			case "ignored_nested_member":
				changed = duplicateGenesisMember(t, ignoredGenesisValue(t, original, []byte(`{"same":0}`)), []string{"releaseFixtureOnly"}, "same", "")
			case "invalid_utf8_ignored_value":
				changed = ignoredGenesisValue(t, original, []byte{'"', 0xff, '"'})
			case "excessive_ignored_nesting":
				changed = ignoredGenesisValue(t, original, []byte(strings.Repeat("[", 70)+"0"+strings.Repeat("]", 70)))
			case "excessive_ignored_structure":
				changed = ignoredGenesisValue(t, original, []byte("["+strings.Repeat("0,", 262144)+"0]"))
			default:
				t.Fatal("unknown genesis JSON control")
			}
			// The previous decoder still sees exactly the advertised block and
			// execution rules. A wrong hash, rules, pin or signature cannot be
			// mistaken for a successful ambiguity refusal.
			var after core.Genesis
			if err := json.Unmarshal(changed, &after); err != nil || !reflect.DeepEqual(before.Config, after.Config) {
				t.Fatal("legacy genesis decoding or execution rules changed")
			}
			beforeHash, e1 := genesisHash(&before)
			afterHash, e2 := genesisHash(&after)
			if e1 != nil || e2 != nil || beforeHash != afterHash {
				t.Fatal("legacy genesis header changed")
			}
			if err := os.WriteFile(file, changed, 0600); err != nil {
				t.Fatal(err)
			}
			pin.Bytes, pin.SHA256 = int64(len(changed)), digest(changed)
			signatures := f.signatures(t)
			if count, err := f.manifest.VerifySignatures(f.policy, signatures, f.keys); err != nil || count != 2 {
				t.Fatal("control signatures did not verify")
			}
			if count, err := f.manifest.VerifyFiles(f.files); err != nil || count != 7 {
				t.Fatal("control file bytes did not verify")
			}
			t.Log("GENESIS-JSON-PRECONDITIONS pass=true fixtureSignatures=2 files=7 sameDecodedHeaderAndRules=true")
			if _, err := f.manifest.Verify(context.Background(), f.policy, signatures, f.keys, f.files, f.sources); err == nil {
				t.Fatal("ambiguous genesis JSON was accepted")
			} else if !strings.Contains(err.Error(), "L2 genesis JSON") {
				t.Fatal("genesis control failed outside the JSON boundary")
			}
			t.Log("GENESIS-JSON-CONTROL pass=true rawBytesAndSignaturesVerified=true decodedHeaderAndRulesPreserved=true runtimeObserved=false")
		})
	}
}
