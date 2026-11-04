package rollup

import (
	"encoding/json"
	"testing"
)

func TestPredeployStateSelection(t *testing.T) {
	for _, value := range []string{"", "source-constructors-v1"} {
		t.Run("accepted/"+value, func(t *testing.T) {
			var selected PredeployState
			raw, _ := json.Marshal(value)
			if err := json.Unmarshal(raw, &selected); err != nil || string(selected) != value {
				t.Fatalf("selection differs: %q %v", selected, err)
			}
			if err := (&Config{PredeployState: selected}).CheckForks(); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, raw := range []string{`null`, `0`, `true`, `{}`, `[]`, `"latest"`, `"SOURCE-CONSTRUCTORS-V1"`, `"source-constructors-v1 "`, `"source-constructors-v2"`} {
		t.Run("refused/"+raw, func(t *testing.T) {
			selected := PredeployStateLegacy
			if json.Unmarshal([]byte(raw), &selected) == nil || selected != PredeployStateLegacy {
				t.Fatal("invalid selection changed the recipe")
			}
		})
	}
	if (&Config{PredeployState: PredeployState("unknown")}).CheckForks() == nil {
		t.Fatal("direct unknown selection accepted")
	}
}

func TestPredeployStateConfigBinding(t *testing.T) {
	base := `{"l1StartHeight":1,"l2ChainId":7340470,"gasLimit":300000000}`
	legacy, err := Parse([]byte(base))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.PredeployState != PredeployStateLegacy {
		t.Fatal("legacy recipe changed")
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields["predeployState"] != nil {
		t.Fatal("omitted recipe gained a serialized field")
	}
	for _, value := range []string{`"source-constructors-v1"`, `null`, `"unknown"`} {
		input := base[:len(base)-1] + `,"predeployState":` + value + `}`
		selected, err := Parse([]byte(input))
		if value == `"source-constructors-v1"` {
			if err != nil || selected.PredeployState != PredeployStateConstructorsV1 {
				t.Fatal("explicit selected configuration failed", err)
			}
		} else if err == nil {
			t.Fatal("unaccepted configuration recipe succeeded")
		}
	}
}
