// Package utxo implements BTC UTXO set management: selection, locking, spending,
// and reorg restoration.
package utxo

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/ai-crypto-onramp/wallet-manager/internal/storage"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Service manages the BTC UTXO set per wallet.
type Service struct {
	Store storage.Store
}

// NewService constructs a UTXOService.
func NewService(st storage.Store) *Service {
	return &Service{Store: st}
}

// SelectForAmount greedily selects free UTXOs whose total value >= amount
// (minor units), atomically marks them locked, and returns the selected
// outpoints and their total. Returns an error if insufficient funds.
func (s *Service) SelectForAmount(ctx context.Context, walletID uuid.UUID, amount decimal.Decimal) ([]string, decimal.Decimal, error) {
	free, err := s.Store.ListFreeUTXOs(ctx, walletID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	// greedy largest-first for fewer inputs
	sort.Slice(free, func(i, j int) bool {
		return parseDec(free[i].Value).GreaterThan(parseDec(free[j].Value))
	})
	var selected []string
	total := decimal.Zero
	for _, u := range free {
		if total.GreaterThanOrEqual(amount) {
			break
		}
		selected = append(selected, u.Outpoint)
		total = total.Add(parseDec(u.Value))
	}
	if total.LessThan(amount) {
		return nil, decimal.Zero, fmt.Errorf("insufficient funds: need %s have %s", amount.String(), total.String())
	}
	if err := s.Store.LockUTXOs(ctx, selected); err != nil {
		return nil, decimal.Zero, fmt.Errorf("lock utxos: %w", err)
	}
	return selected, total, nil
}

// Unlock releases locked UTXOs back to free.
func (s *Service) Unlock(ctx context.Context, outpoints []string) error {
	return s.Store.RestoreUTXOs(ctx, outpoints)
}

// MarkSpent marks the given outpoints as spent with the broadcast tx hash.
func (s *Service) MarkSpent(ctx context.Context, outpoints []string, txHash string) error {
	return s.Store.MarkUTXOsSpent(ctx, outpoints, txHash)
}

// RestoreOnReorg flips spent UTXOs back to free.
func (s *Service) RestoreOnReorg(ctx context.Context, outpoints []string) error {
	return s.Store.RestoreUTXOs(ctx, outpoints)
}

// PruneFinalized deletes/archives finalized spent UTXOs.
func (s *Service) PruneFinalized(ctx context.Context, outpoints []string) error {
	return s.Store.PruneUTXOs(ctx, outpoints)
}

// TrackUTXO inserts a new UTXO into the set.
func (s *Service) TrackUTXO(ctx context.Context, u *storage.UTXO) error {
	return s.Store.InsertUTXO(ctx, u)
}

// parseDec parses a fixed-point decimal string into decimal.Decimal. Returns
// zero on empty/invalid input. Money is stored as NUMERIC(38,18) in Postgres
// and string in memstore; int64 parsing would overflow for large BTC satoshi
// amounts.
func parseDec(s string) decimal.Decimal {
	if s == "" {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero
	}
	return d
}

// SelectMutex is a per-wallet selection mutex used by tests to assert
// correctness under concurrency; production relies on DB row locks.
type SelectMutex struct {
	mu sync.Mutex
}

// Lock acquires the mutex.
func (m *SelectMutex) Lock()   { m.mu.Lock() }

// Unlock releases the mutex.
func (m *SelectMutex) Unlock() { m.mu.Unlock() }