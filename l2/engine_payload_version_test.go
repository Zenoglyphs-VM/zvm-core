package l2

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/Zenoglyphs-VM/zvm-core/rollup"
	"github.com/ethereum/go-ethereum/beacon/engine"
	"github.com/ethereum/go-ethereum/rpc"
)

func payloadMethodServer(t *testing.T, refuse bool) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	methods := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		mu.Lock()
		methods = append(methods, request.Method)
		mu.Unlock()
		// A typed minimal envelope exercises the real required JSON fields. It is
		// not offered as an executed or consensus-valid block.
		envelope := engine.ExecutionPayloadEnvelope{ExecutionPayload: &engine.ExecutableData{
			LogsBloom: make([]byte, 256), ExtraData: []byte{}, BaseFeePerGas: big.NewInt(0), Transactions: [][]byte{},
		}, BlockValue: big.NewInt(0)}
		response := map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": envelope}
		if refuse {
			delete(response, "result")
			response["error"] = map[string]any{"code": -38005, "message": "Unsupported fork"}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, methods...) }
}

func TestEnginePayloadForkVersion(t *testing.T) {
	for _, test := range []struct{ name, profile, method string }{{"isthmus", rollup.EVMIsthmus, "engine_getPayloadV4"}, {"karst", rollup.EVMKarst, "engine_getPayloadV5"}} {
		t.Run(test.name, func(t *testing.T) {
			url, calls := payloadMethodServer(t, false)
			e, err := DialEngineForConfig(context.Background(), url, [32]byte{}, &rollup.Config{EVM: test.profile})
			if err != nil {
				t.Fatal(err)
			}
			defer e.Close()
			// The synthetic envelope tests RPC method selection, not block execution.
			if _, err := e.GetPayload(context.Background(), engine.PayloadID{}); err != nil {
				t.Fatal(err)
			}
			if got := calls(); !reflect.DeepEqual(got, []string{test.method}) {
				t.Fatalf("RPC calls %v, want %s once", got, test.method)
			}
		})
	}
}
func TestEnginePayloadLegacyVersion(t *testing.T) {
	url, calls := payloadMethodServer(t, false)
	e, err := DialEngine(context.Background(), url, [32]byte{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if _, err := e.GetPayload(context.Background(), engine.PayloadID{}); err != nil {
		t.Fatal(err)
	}
	if got := calls(); !reflect.DeepEqual(got, []string{"engine_getPayloadV4"}) {
		t.Fatalf("legacy RPC calls %v", got)
	}
}
func TestEnginePayloadRefusalNotRetried(t *testing.T) {
	url, calls := payloadMethodServer(t, true)
	e, err := DialEngineForConfig(context.Background(), url, [32]byte{}, &rollup.Config{EVM: rollup.EVMKarst})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	_, err = e.GetPayload(context.Background(), engine.PayloadID{})
	var refusal rpc.Error
	if !errors.As(err, &refusal) || refusal.ErrorCode() != -38005 {
		t.Fatalf("RPC refusal was not preserved: %v", err)
	}
	if got := calls(); !reflect.DeepEqual(got, []string{"engine_getPayloadV5"}) {
		t.Fatalf("refused RPC was retried or downgraded: %v", got)
	}
}
func TestDialEngineRejectsMissingOrUnknownProfile(t *testing.T) {
	url, calls := payloadMethodServer(t, false)
	for _, cfg := range []*rollup.Config{nil, {EVM: "unknown-execution-profile"}} {
		e, err := DialEngineForConfig(context.Background(), url, [32]byte{}, cfg)
		if err == nil || e != nil {
			if e != nil {
				e.Close()
			}
			t.Fatal("missing or unsupported execution profile was accepted")
		}
	}
	if len(calls()) != 0 {
		t.Fatal("invalid profile reached the RPC server")
	}
}
