package rollup

import (
	"bytes"
	"fmt"
)

// PredeployState names an explicit genesis-state recipe. Selecting another
// recipe changes the genesis hash and requires a separately accepted epoch.
type PredeployState string

const (
	PredeployStateLegacy         PredeployState = ""
	PredeployStateConstructorsV1 PredeployState = "source-constructors-v1"
)

func (s PredeployState) Validate() error {
	switch s {
	case PredeployStateLegacy, PredeployStateConstructorsV1:
		return nil
	default:
		return fmt.Errorf("predeployState %q: unsupported genesis-state recipe", s)
	}
}

func (s *PredeployState) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("predeployState must be an explicit string")
	}
	var value string
	if err := decodeConfigJSON(raw, &value); err != nil {
		return err
	}
	selected := PredeployState(value)
	if err := selected.Validate(); err != nil {
		return err
	}
	*s = selected
	return nil
}
