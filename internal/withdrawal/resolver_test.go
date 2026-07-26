package withdrawal

import (
	"context"
	"testing"
	"time"

	"github.com/ai-crypto-onramp/wallet-manager/internal/cache"
	"github.com/ai-crypto-onramp/wallet-manager/internal/deriver"
	"github.com/ai-crypto-onramp/wallet-manager/internal/domain"
	"github.com/ai-crypto-onramp/wallet-manager/internal/storage/memstore"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/google/uuid"
)

// TestDeriverKeyResolver_BTCPubKeyHash verifies that the resolver derives the
// real P2WPKH pubkey hash + compressed pubkey from the wallet's active
// address derivation path using the deriver registry.
func TestDeriverKeyResolver_BTCPubKeyHash(t *testing.T) {
	const btcXpub = "xpub6C1HVMz946r433QEjZGpYYWYcspxXXBPys5PBGkmQboRXE6RLfFiStEkKbWKCZaPgDrzZh9nUEunxuiuy6MNdw23du2Ek7GoKYMJVH8eK5E"
	btc, err := deriver.NewBTC(btcXpub, &chaincfg.MainNetParams, cache.NewMem(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reg := deriver.NewRegistry(nil, nil, btc)
	st := memstore.New()

	wID := uuid.New()
	addr := &domain.Address{
		ID: uuid.New(), WalletID: wID, Chain: domain.ChainBitcoin,
		Address:        "bc1q3yg7x8zph0qshlg00k5q8xq3nhqr2umyqy98sh",
		DerivationPath: "m/84'/0'/0'/0/0", Index: 0, Change: 0,
		State: domain.AddressStateActive,
	}
	if err := st.InsertAddress(context.Background(), addr); err != nil {
		t.Fatal(err)
	}

	r := NewDeriverKeyResolver(st, reg)
	bk, err := r.BTCPubKeyHash(context.Background(), wID)
	if err != nil {
		t.Fatalf("BTCPubKeyHash: %v", err)
	}
	if bk.PubKeyHash == ([20]byte{}) {
		t.Fatal("expected non-zero pubkey hash")
	}
	if len(bk.CompressedPubKey) != 33 {
		t.Fatalf("expected 33-byte compressed pubkey, got %d", len(bk.CompressedPubKey))
	}
	// Re-derive directly via the registry and confirm equality.
	wantHash, wantPub, err := reg.BTCPubKeyHashFor("m/84'/0'/0'/0/0")
	if err != nil {
		t.Fatal(err)
	}
	if bk.PubKeyHash != wantHash {
		t.Errorf("pubkey hash mismatch: got %x want %x", bk.PubKeyHash, wantHash)
	}
	if string(bk.CompressedPubKey) != string(wantPub) {
		t.Errorf("compressed pubkey mismatch: got %x want %x", bk.CompressedPubKey, wantPub)
	}
}

// TestDeriverKeyResolver_NoActiveAddressReturnsZero verifies the DEV_MODE
// fallback: when no active address is stored, the resolver returns a zero
// BTCKeys/SolanaKeys so the builder uses the placeholder path.
func TestDeriverKeyResolver_NoActiveAddressReturnsZero(t *testing.T) {
	const btcXpub = "xpub6C1HVMz946r433QEjZGpYYWYcspxXXBPys5PBGkmQboRXE6RLfFiStEkKbWKCZaPgDrzZh9nUEunxuiuy6MNdw23du2Ek7GoKYMJVH8eK5E"
	btc, err := deriver.NewBTC(btcXpub, &chaincfg.MainNetParams, cache.NewMem(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	reg := deriver.NewRegistry(nil, nil, btc)
	st := memstore.New()

	r := NewDeriverKeyResolver(st, reg)
	bk, err := r.BTCPubKeyHash(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("expected nil err on missing address, got %v", err)
	}
	if bk.PubKeyHash != ([20]byte{}) || bk.CompressedPubKey != nil {
		t.Errorf("expected zero BTCKeys on missing address, got %+v", bk)
	}
	sk, err := r.SolanaFrom(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("expected nil err on missing solana address, got %v", err)
	}
	if sk.From != ([32]byte{}) {
		t.Errorf("expected zero SolanaKeys on missing address, got %+v", sk)
	}
}
