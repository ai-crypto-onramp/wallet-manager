package deriver

import (
	"context"
	"fmt"
	"time"

	"github.com/ai-crypto-onramp/wallet-manager/internal/cache"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/bech32"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/google/uuid"
)

// BTCDeriver derives native SegWit (BIP-84) bech32 Bitcoin addresses from an
// account-level extended public key.
type BTCDeriver struct {
	xpub  string
	net   *chaincfg.Params
	cache cache.Cache
	ttl   time.Duration
}

// NewBTC creates a BTC deriver. For mainnet use &chaincfg.MainNetParams; for
// testnet use &chaincfg.TestNet3Params.
func NewBTC(xpub string, net *chaincfg.Params, c cache.Cache, ttl time.Duration) (*BTCDeriver, error) {
	x, err := ParseXpub(xpub)
	if err != nil {
		return nil, err
	}
	if net == nil {
		net = &chaincfg.MainNetParams
	}
	return &BTCDeriver{xpub: x, net: net, cache: c, ttl: ttl}, nil
}

// DeriveNext derives the next address for the given change chain (0=receive,1=change).
func (d *BTCDeriver) DeriveNext(ctx context.Context, walletID uuid.UUID, chain Chain, change int) (Result, error) {
	if chain != ChainBitcoin {
		return Result{}, fmt.Errorf("non-BTC chain %q passed to BTCDeriver", chain)
	}
	if change != 0 && change != 1 {
		return Result{}, fmt.Errorf("BTC change chain must be 0 or 1")
	}
	return Result{}, fmt.Errorf("DeriveNext requires an index for BTC; use DeriveAt")
}

// DeriveAt derives the bech32 native SegWit address at the given index/change.
func (d *BTCDeriver) DeriveAt(ctx context.Context, walletID uuid.UUID, chain Chain, index, change int) (Result, error) {
	if chain != ChainBitcoin {
		return Result{}, fmt.Errorf("non-BTC chain %q passed to BTCDeriver", chain)
	}
	if change != 0 && change != 1 {
		return Result{}, fmt.Errorf("BTC change chain must be 0 or 1")
	}
	if index < 0 {
		return Result{}, fmt.Errorf("BTC index must be >= 0")
	}
	path := fmt.Sprintf("m/84'/0'/0'/%d/%d", change, index)
	cacheKey := fmt.Sprintf("deriv:btc:%s:%d:%d", d.xpub, change, index)
	if v, ok, err := d.cache.Get(ctx, cacheKey); err == nil && ok {
		return d.makeAddr(walletID, v, path, index, change), nil
	}
	addr, err := d.deriveUncached(index, change)
	if err != nil {
		return Result{}, err
	}
	_ = d.cache.Set(ctx, cacheKey, addr, d.ttl)
	return d.makeAddr(walletID, addr, path, index, change), nil
}

func (d *BTCDeriver) makeAddr(_ uuid.UUID, addr, path string, index, change int) Result {
	return Result{
		Address:        addr,
		DerivationPath: path,
		Index:          index,
		Change:         change,
	}
}

func (d *BTCDeriver) deriveUncached(index, change int) (string, error) {
	pub, err := d.deriveChildPubKey(index, change)
	if err != nil {
		return "", err
	}
	witness := append([]byte{0x00}, btcutil.Hash160(pub.SerializeCompressed())...)
	return bech32Address("bc", witness)
}

// deriveChildPubKey derives the compressed secp256k1 public key at
// m/<change>/<index> under the account xpub.
func (d *BTCDeriver) deriveChildPubKey(index, change int) (*btcec.PublicKey, error) {
	acc, err := hdkeychain.NewKeyFromString(d.xpub)
	if err != nil {
		return nil, fmt.Errorf("parse xpub: %w", err)
	}
	changeIdx, err := nonHardened(change)
	if err != nil {
		return nil, err
	}
	changeBranch, err := acc.Derive(changeIdx)
	if err != nil {
		return nil, fmt.Errorf("derive change chain %d: %w", change, err)
	}
	childIdx, err := nonHardened(index)
	if err != nil {
		return nil, err
	}
	child, err := changeBranch.Derive(childIdx)
	if err != nil {
		return nil, fmt.Errorf("derive index %d: %w", index, err)
	}
	pub, err := child.ECPubKey()
	if err != nil {
		return nil, fmt.Errorf("ec pubkey: %w", err)
	}
	return pub, nil
}

// PubKeyHashFor returns the 20-byte P2WPKH witness program hash (RIPEMD-160
// of SHA-256 of the compressed pubkey) and the 33-byte compressed pubkey
// for the derived child at (change, index). Used by the withdrawal builder
// to compute the real sighash against the wallet's UTXO script.
func (d *BTCDeriver) PubKeyHashFor(change, index int) ([20]byte, []byte, error) {
	pub, err := d.deriveChildPubKey(index, change)
	if err != nil {
		return [20]byte{}, nil, err
	}
	compressed := pub.SerializeCompressed()
	var hash [20]byte
	copy(hash[:], btcutil.Hash160(compressed))
	return hash, compressed, nil
}

// bech32Address converts a 21-byte witness program (version+20-byte hash) into
// a bech32 SegWit address with the given human-readable part.
func bech32Address(hrp string, witness []byte) (string, error) {
	conv, err := bech32.ConvertBits(witness[1:], 8, 5, true)
	if err != nil {
		return "", err
	}
	return bech32.Encode(hrp, append([]byte{witness[0]}, conv...))
}
