package withdrawal

import (
	"context"
	"fmt"

	"github.com/ai-crypto-onramp/wallet-manager/internal/deriver"
	"github.com/ai-crypto-onramp/wallet-manager/internal/storage"
	"github.com/btcsuite/btcd/btcutil/base58"
	"github.com/google/uuid"
)

// DeriverKeyResolver implements BTCKeysResolver and SolanaKeysResolver using
// the deriver registry (for BTC xpub derivation) and the storage store (for
// the active address record carrying the derivation path / base58 pubkey).
type DeriverKeyResolver struct {
	Store    storage.Store
	Derivers *deriver.Registry
}

// NewDeriverKeyResolver wires the resolver to a store and deriver registry.
func NewDeriverKeyResolver(st storage.Store, reg *deriver.Registry) *DeriverKeyResolver {
	return &DeriverKeyResolver{Store: st, Derivers: reg}
}

// BTCPubKeyHash derives the wallet's P2WPKH pubkey hash and compressed pubkey
// from the account xpub at the active address's derivation path. Returns a
// zero BTCKeys (triggering the DEV_MODE placeholder fallback in the builder)
// if no active address is stored or the deriver registry is nil.
func (r *DeriverKeyResolver) BTCPubKeyHash(ctx context.Context, walletID uuid.UUID) (BTCKeys, error) {
	if r == nil || r.Derivers == nil || r.Store == nil {
		return BTCKeys{}, nil
	}
	addr, err := r.Store.GetActiveAddress(ctx, walletID)
	if err != nil {
		return BTCKeys{}, nil
	}
	if addr.DerivationPath == "" {
		return BTCKeys{}, nil
	}
	pkHash, compressed, err := r.Derivers.BTCPubKeyHashFor(addr.DerivationPath)
	if err != nil {
		return BTCKeys{}, fmt.Errorf("derive btc pubkey hash for wallet %s at %s: %w", walletID, addr.DerivationPath, err)
	}
	return BTCKeys{PubKeyHash: pkHash, CompressedPubKey: compressed}, nil
}

// SolanaFrom decodes the wallet's base58 ed25519 pubkey from the active
// address record. Returns a zero SolanaKeys (triggering the DEV_MODE
// placeholder fallback in the builder) if no active address is stored.
func (r *DeriverKeyResolver) SolanaFrom(ctx context.Context, walletID uuid.UUID) (SolanaKeys, error) {
	if r == nil || r.Store == nil {
		return SolanaKeys{}, nil
	}
	addr, err := r.Store.GetActiveAddress(ctx, walletID)
	if err != nil {
		return SolanaKeys{}, nil
	}
	if addr.Address == "" {
		return SolanaKeys{}, nil
	}
	decoded := base58.Decode(addr.Address)
	if len(decoded) != 32 {
		return SolanaKeys{}, fmt.Errorf("solana: wallet %s active address %q decodes to %d bytes, want 32", walletID, addr.Address, len(decoded))
	}
	var out SolanaKeys
	copy(out.From[:], decoded)
	return out, nil
}

// Ensure the resolver satisfies both interfaces at compile time.
var (
	_ BTCKeysResolver    = (*DeriverKeyResolver)(nil)
	_ SolanaKeysResolver = (*DeriverKeyResolver)(nil)
)
