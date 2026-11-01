//go:build fullclient_recovery

package orderbook

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// CoreChunkReadFixture delegates to the original synthetic source handler.
// It counts actual production requests for the exact blocked momentum, rather
// than treating elapsed time or a repeated log line as proof of a retry.
type CoreChunkReadFixture struct {
	*FullClientSignedRecoveryFixture
	source *httptest.Server
	reads  atomic.Uint64
}

func NewCoreChunkReadFixture(t *testing.T, f *FullClientSignedRecoveryFixture) *CoreChunkReadFixture {
	t.Helper()
	wrapped := &CoreChunkReadFixture{FullClientSignedRecoveryFixture: f}
	wrapped.source = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(req.Body, 8193))
		req.Body.Close()
		var call struct {
			Method string
			Params []json.RawMessage
		}
		if err != nil || len(raw) > 8192 || json.Unmarshal(raw, &call) != nil {
			http.Error(w, "invalid bounded source request", http.StatusBadRequest)
			return
		}
		var start, count uint64
		if call.Method == "ledger.getDetailedMomentumsByHeight" && len(call.Params) == 2 &&
			json.Unmarshal(call.Params[0], &start) == nil && json.Unmarshal(call.Params[1], &count) == nil && start == 8 && count == 1 {
			wrapped.reads.Add(1)
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
		f.r.nomReply(w, req)
	}))
	t.Cleanup(wrapped.source.Close)
	path := filepath.Join(f.Directory(), "core-chunk-observer.enable")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write([]byte("core chunk observer v1\n"))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("observer declaration write failed")
	}
	return wrapped
}

func (f *CoreChunkReadFixture) Args() []string {
	args := f.FullClientSignedRecoveryFixture.Args()
	args[7] = f.source.URL
	return args
}

func (f *CoreChunkReadFixture) ChunkReadAttempts() uint64 { return f.reads.Load() }

// Inject only after the child has opened its real service/store at momentum 7.
// None of these changes touch the immutable backup or its original source.
func (f *CoreChunkReadFixture) InjectChunkReadFault(kind string) {
	t := f.r.t
	t.Helper()
	uri := url.URL{Scheme: "file", Path: filepath.Join(f.Directory(), "node.db")}
	uri.RawQuery = "mode=rw&_pragma=busy_timeout(15000)"
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var hashes []string
	rows, err := db.Query("SELECT hash FROM chunks WHERE momentum=7 AND consumed_at IS NULL")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, hash)
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil || len(hashes) != 1 {
		t.Fatal("fault requires the single original unconsumed chunk")
	}
	var result sql.Result
	switch kind {
	case "invalid-author":
		result, err = db.Exec("UPDATE chunks SET author='invalid fixture address' WHERE hash=?", hashes[0])
	case "invalid-momentum":
		result, err = db.Exec("UPDATE chunks SET momentum='invalid fixture height' WHERE hash=?", hashes[0])
	case "missing-table":
		_, err = db.Exec("ALTER TABLE chunks RENAME TO core_retained_chunks")
	default:
		t.Fatal("undeclared chunk read fault")
	}
	if err != nil {
		t.Fatal(err)
	}
	if result != nil {
		n, err := result.RowsAffected()
		if err != nil || n != 1 {
			t.Fatal("fault did not affect exactly one original row")
		}
	}
}

type CoreChunkReadObservation struct {
	RPCNumber    uint64
	DirectNumber uint64
	RPCHash      string
	DirectHash   string
}

func (f *CoreChunkReadFixture) DerivedChunkReadHead() (uint64, uint64, string, int) {
	t := f.r.t
	t.Helper()
	var height, number uint64
	var hash string
	var invalid int
	if err := f.read.QueryRow("SELECT height,l2_number,l2_hash FROM momentums ORDER BY height DESC LIMIT 1").Scan(&height, &number, &hash); err != nil {
		t.Fatal(err)
	}
	if err := f.read.QueryRow("SELECT count(*) FROM messages WHERE momentum=8 AND kind='assemble' AND status='invalid'").Scan(&invalid); err != nil {
		t.Fatal(err)
	}
	return height, number, hash, invalid
}

// The observer is test-only and read-only until an explicit owned exit request.
// Its unlogged random capability is confined to loopback and a fixture file.
// It never changes an Engine payload, head, SQL row, order, source or retry loop.
func startCoreChunkReadObserver(t *testing.T, f *FullClientRecoveryCLIFixture, directory string) {
	t.Helper()
	path := filepath.Join(directory, "core-chunk-observer.enable")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len("core chunk observer v1\n")) {
		t.Fatal("invalid observer declaration")
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "core chunk observer v1\n" {
		t.Fatal("observer declaration differs")
	}
	var capability [16]byte
	if _, err := rand.Read(capability[:]); err != nil {
		t.Fatal(err)
	}
	prefix := "/" + hex.EncodeToString(capability[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		exit := req.URL.Path == prefix+"/exit" && req.Method == http.MethodPost
		if !exit && (req.URL.Path != prefix+"/head" || req.Method != http.MethodGet) {
			http.NotFound(w, req)
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		head, err := f.r.eth.HeaderByNumber(ctx, nil)
		direct := f.r.backend.chain.CurrentBlock()
		if err != nil || head == nil || direct == nil {
			http.Error(w, "actual engine observation failed", http.StatusServiceUnavailable)
			return
		}
		observed := CoreChunkReadObservation{head.Number.Uint64(), direct.Number.Uint64(), head.Hash().Hex(), direct.Hash().Hex()}
		if exit {
			if observed.RPCNumber != 6 || observed.DirectNumber != 6 || observed.RPCHash != observed.DirectHash {
				fmt.Println("core_chunk_read_exit_refused engine_head_mismatch=true")
				os.Exit(88)
			}
			fmt.Println("core_chunk_read_owned_exit engine_head=6 rpc_and_direct_hashes_equal=true actual_exit=86")
			fmt.Println(`{"owned_cli_boundary":"","core_chunk_read_observer":true,"actual_exit":86}`)
			os.Exit(86)
		}
		if err := json.NewEncoder(w).Encode(observed); err != nil {
			return
		}
	}))
	t.Cleanup(server.Close)
	file, err := os.OpenFile(filepath.Join(directory, "core-chunk-observer.endpoint"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write([]byte(server.URL + prefix))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("observer endpoint publication failed")
	}
}
