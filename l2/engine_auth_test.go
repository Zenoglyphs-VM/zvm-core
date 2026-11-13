package l2

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// Check the wire token independently of the JWT library used by the client.
func engineTokenIssuedAt(header http.Header, secret [32]byte) (int64, error) {
	values := header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return 0, fmt.Errorf("want one Bearer authorization value")
	}
	parts := strings.Split(strings.TrimPrefix(values[0], "Bearer "), ".")
	if len(parts) != 3 {
		return 0, fmt.Errorf("want three JWT segments")
	}
	metadata, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, err
	}
	var fields map[string]string
	if err := json.Unmarshal(metadata, &fields); err != nil || len(fields) != 2 || fields["alg"] != "HS256" || fields["typ"] != "JWT" {
		return 0, fmt.Errorf("want the HS256 JWT header")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return 0, err
	}
	mac := hmac.New(sha256.New, secret[:])
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return 0, fmt.Errorf("signature does not match the selected secret")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, err
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payload, &claims); err != nil || len(claims) != 1 {
		return 0, fmt.Errorf("want only the issued-at claim")
	}
	var issued int64
	if err := json.Unmarshal(claims["iat"], &issued); err != nil {
		return 0, err
	}
	return issued, nil
}

func TestEngineAuthRefreshesIssuedAtAndReplacesAuthorization(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		secret := [32]byte{1, 2, 3}
		auth := engineAuth(secret)
		header := http.Header{"Authorization": {"old", "duplicate"}, "X-Other": {"preserved"}}
		for attempt := 0; attempt < 2; attempt++ {
			if err := auth(header); err != nil {
				t.Fatal(err)
			}
			issued, err := engineTokenIssuedAt(header, secret)
			if err != nil || issued != time.Now().Unix() {
				t.Fatalf("request %d: issued=%d err=%v", attempt, issued, err)
			}
			if header.Get("X-Other") != "preserved" {
				t.Fatal("unrelated header changed")
			}
			otherSecret := secret
			otherSecret[31] = 1
			if _, err := engineTokenIssuedAt(header, otherSecret); err == nil {
				t.Fatal("token accepted with a different secret")
			}
			time.Sleep(2 * time.Second) // Virtual time; no wall-clock delay.
		}
	})
}

func TestEngineAuthConcurrentRequests(t *testing.T) {
	secret := [32]byte{4, 5, 6}
	auth := engineAuth(secret)
	var requests sync.WaitGroup
	for i := 0; i < 32; i++ {
		requests.Add(1)
		go func() {
			defer requests.Done()
			header := make(http.Header)
			if err := auth(header); err != nil {
				t.Error(err)
				return
			}
			if _, err := engineTokenIssuedAt(header, secret); err != nil {
				t.Error(err)
			}
		}()
	}
	requests.Wait()
}

func TestDialEngineAuthenticatesHTTPRequests(t *testing.T) {
	secret := [32]byte{7, 8, 9}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		issued, err := engineTokenIssuedAt(r.Header, secret)
		if err != nil || issued < time.Now().Add(-time.Minute).Unix() || issued > time.Now().Unix() {
			t.Errorf("invalid Engine authorization: issued=%d err=%v", issued, err)
			http.Error(w, "invalid authorization", http.StatusUnauthorized)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Method != "engine_authProbe" {
			t.Error("unexpected JSON-RPC request")
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": "ok"})
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	engine, err := DialEngine(ctx, server.URL, secret)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	for i := 0; i < 2; i++ {
		var result string
		if err := engine.c.CallContext(ctx, &result, "engine_authProbe"); err != nil || result != "ok" {
			t.Fatalf("request %d: result=%q err=%v", i, result, err)
		}
	}
}
