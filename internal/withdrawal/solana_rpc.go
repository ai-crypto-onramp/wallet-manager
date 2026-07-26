package withdrawal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcutil/base58"
)

// SolanaBlockhashFetcher returns a recent Solana blockhash valid for signing.
// Implementations should cache with a short TTL (blockhashes expire ~60-90
// slots, ~50s on mainnet).
type SolanaBlockhashFetcher interface {
	FetchRecentBlockhash(ctx context.Context) ([32]byte, error)
}

// solanaBlockhashCacheTTL is how long a fetched blockhash is reused before
// re-fetching. Solana blockhashes expire after ~60-90 slots (~50s on mainnet);
// 30s keeps us safely inside the validity window while minimising RPC calls.
const solanaBlockhashCacheTTL = 30 * time.Second

// SolanaRPCBlockhashFetcher fetches a recent blockhash from a Solana JSON-RPC
// endpoint via getLatestBlockhash, caching the result for a short TTL.
type SolanaRPCBlockhashFetcher struct {
	rpcURL string
	client *http.Client

	mu       sync.Mutex
	cached   [32]byte
	cachedAt time.Time
}

// NewSolanaRPCBlockhashFetcher constructs a fetcher targeting the given RPC
// URL (e.g. https://api.mainnet-beta.solana.com). An empty rpcURL yields a
// fetcher whose FetchRecentBlockhash always returns ErrNoSolanaRPC; the
// caller is expected to gate this behind DEV_MODE/prod at wiring time.
func NewSolanaRPCBlockhashFetcher(rpcURL string) *SolanaRPCBlockhashFetcher {
	return &SolanaRPCBlockhashFetcher{
		rpcURL: rpcURL,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// ErrNoSolanaRPC is returned when no Solana RPC URL is configured.
var ErrNoSolanaRPC = fmt.Errorf("solana: no RPC URL configured")

func (f *SolanaRPCBlockhashFetcher) FetchRecentBlockhash(ctx context.Context) ([32]byte, error) {
	if f.rpcURL == "" {
		return [32]byte{}, ErrNoSolanaRPC
	}
	f.mu.Lock()
	if !f.cachedAt.IsZero() && time.Since(f.cachedAt) < solanaBlockhashCacheTTL && f.cached != ([32]byte{}) {
		out := f.cached
		f.mu.Unlock()
		return out, nil
	}
	f.mu.Unlock()

	reqBody := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "getLatestBlockhash",
		"params":  []any{},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return [32]byte{}, fmt.Errorf("marshal getLatestBlockhash: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.rpcURL, bytes.NewReader(body))
	if err != nil {
		return [32]byte{}, fmt.Errorf("build rpc request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return [32]byte{}, fmt.Errorf("solana rpc: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return [32]byte{}, fmt.Errorf("solana rpc: HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var parsed struct {
		Result struct {
			Value struct {
				Blockhash string `json:"blockhash"`
			} `json:"value"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return [32]byte{}, fmt.Errorf("decode getLatestBlockhash: %w", err)
	}
	if parsed.Error != nil {
		return [32]byte{}, fmt.Errorf("solana rpc error: %s", parsed.Error.Message)
	}
	if parsed.Result.Value.Blockhash == "" {
		return [32]byte{}, fmt.Errorf("solana rpc: empty blockhash")
	}
	decoded := base58.Decode(parsed.Result.Value.Blockhash)
	if len(decoded) != 32 {
		return [32]byte{}, fmt.Errorf("solana rpc: blockhash %q decodes to %d bytes, want 32", parsed.Result.Value.Blockhash, len(decoded))
	}
	var out [32]byte
	copy(out[:], decoded)
	f.mu.Lock()
	f.cached = out
	f.cachedAt = time.Now()
	f.mu.Unlock()
	return out, nil
}
