package withdrawal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/btcsuite/btcd/btcutil/base58"
)

func TestSolanaRPCBlockhashFetcher_FetchesRealBlockhash(t *testing.T) {
	want := [32]byte{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf, 0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["method"] != "getLatestBlockhash" {
			t.Errorf("expected method getLatestBlockhash, got %v", req["method"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"result": map[string]any{
				"value": map[string]any{
					"blockhash": base58.Encode(want[:]),
				},
			},
		})
	}))
	defer srv.Close()

	f := NewSolanaRPCBlockhashFetcher(srv.URL)
	got, err := f.FetchRecentBlockhash(context.Background())
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got != want {
		t.Errorf("expected %x, got %x", want, got)
	}

	// Second call within TTL must hit the cache (no HTTP request needed);
	// close the server to prove it.
	srv.Close()
	got2, err := f.FetchRecentBlockhash(context.Background())
	if err != nil {
		t.Fatalf("cached fetch: %v", err)
	}
	if got2 != want {
		t.Errorf("cached: expected %x, got %x", want, got2)
	}
}

func TestSolanaRPCBlockhashFetcher_EmptyURLReturnsErrNoSolanaRPC(t *testing.T) {
	f := NewSolanaRPCBlockhashFetcher("")
	if _, err := f.FetchRecentBlockhash(context.Background()); err != ErrNoSolanaRPC {
		t.Errorf("expected ErrNoSolanaRPC, got %v", err)
	}
}
