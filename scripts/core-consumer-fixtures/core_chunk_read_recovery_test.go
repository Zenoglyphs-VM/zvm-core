//go:build fullclient_recovery

package orderbook_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Zenoglyphs-VM/zvm-node/orderbook"
)

func coreChunkObserver(t *testing.T, ctx context.Context, f *orderbook.CoreChunkReadFixture) string {
	t.Helper()
	path := filepath.Join(f.Directory(), "core-chunk-observer.endpoint")
	for {
		info, err := os.Lstat(path)
		if err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= 256 {
			raw, err := os.ReadFile(path)
			u, parseErr := url.Parse(string(raw))
			if err == nil && parseErr == nil && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && u.Port() != "" && len(u.Path) == 33 && u.User == nil && u.RawQuery == "" {
				return string(raw)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("owned full-client observer did not become available")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func coreChunkEngineHead(t *testing.T, ctx context.Context, endpoint string) orderbook.CoreChunkReadObservation {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/head", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal("owned full-client head request failed")
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1025))
	var head orderbook.CoreChunkReadObservation
	if err != nil || response.StatusCode != http.StatusOK || len(raw) > 1024 || json.Unmarshal(raw, &head) != nil || head.RPCHash == "" || head.RPCHash != head.DirectHash || head.RPCNumber != head.DirectNumber {
		t.Fatal("RPC and direct full-client observations disagree")
	}
	return head
}

// The negative baseline executes this exact test with the original Core 29E.
// Candidate qualification requires all three subtests and all retained roots.
func TestCoreChunkReadFullClientRecovery(t *testing.T) {
	reference := orderbook.NewFullClientSignedRecoveryFixture(t)
	p := startOwnedCLI(t, reference, "", 0)
	waitCLIHorizon(t, reference, 7, p)
	p.waitExit(0, true)
	reference.AdmitSignedOrders()
	p = startOwnedCLI(t, reference, "", 1)
	waitCLIHorizon(t, reference, 8, p)
	waitCLIHorizon(t, reference, 9, p)
	p.waitExit(0, true)
	want := reference.SignedSnapshot()
	reference.CloseExecutionClient()
	if t.Failed() {
		t.FailNow()
	}

	source := orderbook.NewFullClientSignedProcessFixture(t)
	p = startOwnedCLIWithTest(t, source, "", 0, "TestFullClientOwnedProcess")
	waitCLIHorizon(t, source, 7, p)
	p.waitExit(0, true)
	orderbook.AssertOwnedFullClientProcess(t, p.output.snapshot(), 0, "chunk-read-prime")
	source.CheckExecutionClientDirectory()
	source.RestartExecutionClient()
	source.AdmitSignedOrders()
	source.CloseExecutionClient()
	backup := source.ColdPersistenceBackup()
	for _, kind := range []string{"invalid-author", "invalid-momentum", "missing-table"} {
		t.Run(kind, func(t *testing.T) {
			fault := orderbook.NewCoreChunkReadFixture(t, orderbook.RestoreFullClientSignedColdBackup(t, backup))
			p := startOwnedCLIWithTest(t, fault, "", 0, "TestFullClientOwnedProcess")
			waitCLIHorizon(t, fault, 7, p)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			endpoint := coreChunkObserver(t, ctx, fault)
			for !strings.Contains(p.output.snapshot(), "resuming") || !strings.Contains(p.output.snapshot(), "momentum=7") {
				select {
				case <-ctx.Done():
					t.Fatal("production derivation did not finish startup at momentum 7")
				case <-time.After(20 * time.Millisecond):
				}
			}
			before := coreChunkEngineHead(t, ctx, endpoint)
			derived, number, hash, invalid := fault.DerivedChunkReadHead()
			if derived != 7 || number != 6 || before.RPCNumber != 6 || before.RPCHash != hash || invalid != 0 {
				t.Fatal("fault did not start at the exact valid paired horizon")
			}
			fault.InjectChunkReadFault(kind)
			fault.Horizon(8)
			for {
				after := coreChunkEngineHead(t, ctx, endpoint)
				derived, number, hash, invalid = fault.DerivedChunkReadHead()
				if after != before || derived != 7 || number != 6 || hash != before.RPCHash || invalid != 0 {
					t.Fatalf("core_chunk_read_advanced source=8 engine=%d derived=%d invalid_assembled=%d", after.RPCNumber, derived, invalid)
				}
				output := p.output.snapshot()
				if fault.ChunkReadAttempts() >= 2 && strings.Contains(output, "derivation stopped; retrying until it clears") && strings.Contains(output, "momentum 8:") && strings.Contains(output, "reading chunk") {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatalf("chunk read failure did not halt and retry the same momentum: attempts=%d output=%s", fault.ChunkReadAttempts(), output)
				case <-time.After(25 * time.Millisecond):
				}
			}
			t.Logf("core_chunk_read_halted fault=%s source_momentum=8 actual_source_requests=%d durable_source_head=7 rpc_and_direct_engine_head=6 hashes_unchanged=true invalid_assembled_messages=0 production_retry_log=true", kind, fault.ChunkReadAttempts())
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/exit", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, requestErr := http.DefaultClient.Do(req)
			if response != nil {
				response.Body.Close()
			}
			if requestErr == nil {
				t.Fatal("owned process exit unexpectedly returned an HTTP response")
			}
			p.waitExit(86, false)
			if !strings.Contains(p.output.snapshot(), "core_chunk_read_owned_exit engine_head=6 rpc_and_direct_hashes_equal=true actual_exit=86") || strings.Contains(p.output.snapshot(), "owned_full_client_closed graceful=true") {
				t.Fatal("chunk fault exit did not skip the actual full-service cleanup")
			}
			fault.CheckExecutionClientDirectory()

			// The fault target is never repaired. Use another independent raw
			// restore of the original valid pair, without re-signing/resubmit.
			restored := orderbook.RestoreFullClientSignedColdBackup(t, backup)
			restored.Horizon(8)
			p = startOwnedCLIWithTest(t, restored, "", 0, "TestFullClientOwnedProcess")
			waitCLIHorizon(t, restored, 8, p)
			waitCLIHorizon(t, restored, 9, p)
			p.waitExit(0, true)
			orderbook.AssertOwnedFullClientProcess(t, p.output.snapshot(), 0, "chunk-read-recovery")
			restored.CheckExecutionClientDirectory()
			restored.RestartExecutionClient()
			orderbook.AssertRecoveryCLIState(t, restored.SignedSnapshot(), want)
			restored.CloseExecutionClient()
			orderbook.AssertFullClientColdBackupUnchanged(t, backup)
			t.Logf("core_chunk_read_recovered fault=%s same_original_momentum=8 final_momentum=9 all_original_canonical_tables_equal=true all_six_categories_equal=true original_EOA_signatures_preserved=true fault_target_repaired=false source_and_backup_unchanged=true NoM_source_synthetic=true kernel_or_power_loss=false actual_Node_dependency_adoption=false full_C02_N04_acceptance=false", kind)
		})
		if t.Failed() {
			t.FailNow()
		}
	}
}
