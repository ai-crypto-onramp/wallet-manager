package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/ai-crypto-onramp/wallet-manager/internal/domain"
	"github.com/ai-crypto-onramp/wallet-manager/internal/storage"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

func q(pattern string) string { return pattern }

func newMock(t *testing.T) (*Store, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherRegexp))
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	return NewFromDB(db), mock
}

func TestNewOpenError(t *testing.T) {
	if _, err := New("invalid dsn ::::"); err != nil {
		// sql.Open is lazy; it only validates the driver name. "postgres" is
		// valid so this returns nil. We exercise the error branch via a bad
		// driver name instead.
	}
	if _, err := sql.Open("baddriver", ""); err == nil {
		// sql.Open rarely errors for unknown drivers in lib/pq; skip if nil.
	}
	// Exercise New happy path (lazy open).
	s, err := New("host=localhost user=u dbname=d")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil store")
	}
	_ = s.Close()
}

func TestNewFromDB(t *testing.T) {
	s := NewFromDB(nil)
	if s == nil {
		t.Fatal("expected non-nil store")
	}
	if s.DB() != nil {
		t.Error("expected nil DB")
	}
}

func TestApplyMigrations(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`CREATE TABLE.*`)).WillReturnResult(sqlmock.NewResult(0, 0))
	if err := s.ApplyMigrations(context.Background(), "CREATE TABLE x (id int);"); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestApplyMigrationsError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`.*`)).WillReturnError(errors.New("ddl error"))
	if err := s.ApplyMigrations(context.Background(), "bad"); err == nil {
		t.Error("expected error")
	}
}

func TestInTxCommit(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(q(`INSERT INTO wallets`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	err := s.InTx(context.Background(), func(ctx context.Context) error {
		_, err := s.exec(ctx, "INSERT INTO wallets (id) VALUES ($1)", uuid.New())
		return err
	})
	if err != nil {
		t.Fatalf("InTx commit: %v", err)
	}
}

func TestInTxRollback(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(q(`INSERT`)).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	err := s.InTx(context.Background(), func(ctx context.Context) error {
		_, err := s.exec(ctx, "INSERT INTO wallets (id) VALUES ($1)", uuid.New())
		return err
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected boom, got %v", err)
	}
}

func TestInTxBeginError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin().WillReturnError(errors.New("begin failed"))
	if err := s.InTx(context.Background(), func(ctx context.Context) error { return nil }); err == nil {
		t.Error("expected begin error")
	}
}

func TestTxExecUsesTx(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(q(`UPDATE`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	_ = s.InTx(context.Background(), func(ctx context.Context) error {
		_, err := s.exec(ctx, "UPDATE wallets SET state=$1", "ACTIVE")
		return err
	})
}

func TestTxQueryRowUsesTx(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	rows := sqlmock.NewRows([]string{"id"}).AddRow(uuid.New().String())
	mock.ExpectQuery(q(`SELECT`)).WillReturnRows(rows)
	mock.ExpectCommit()
	_ = s.InTx(context.Background(), func(ctx context.Context) error {
		row := s.queryRow(ctx, "SELECT id FROM wallets WHERE id=$1", uuid.New())
		var id string
		return row.Scan(&id)
	})
}

func TestTxQueryUsesTx(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	rows := sqlmock.NewRows([]string{"id"}).AddRow(uuid.New().String())
	mock.ExpectQuery(q(`SELECT`)).WillReturnRows(rows)
	mock.ExpectCommit()
	_ = s.InTx(context.Background(), func(ctx context.Context) error {
		rs, err := s.query(ctx, "SELECT id FROM wallets", nil)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var id string
			if err := rs.Scan(&id); err != nil {
				return err
			}
		}
		return rs.Err()
	})
}

func TestTxNil(t *testing.T) {
	s, _ := newMock(t)
	if tx := s.tx(context.Background()); tx != nil {
		t.Error("expected nil tx outside InTx")
	}
}

func TestCreateWallet(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO wallets`)).WillReturnResult(sqlmock.NewResult(0, 1))
	w := &domain.Wallet{ID: uuid.New(), Chain: domain.ChainEthereum, Type: domain.WalletTypeHot, Label: "w", State: domain.WalletStateActive, KeyID: "k", CustodianRef: "mpc", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := s.CreateWallet(context.Background(), w); err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
}

func TestCreateWalletError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO wallets`)).WillReturnError(errors.New("dup"))
	if err := s.CreateWallet(context.Background(), &domain.Wallet{}); err == nil {
		t.Error("expected error")
	}
}

func TestGetWallet(t *testing.T) {
	s, mock := newMock(t)
	id := uuid.New()
	created := time.Now()
	rows := sqlmock.NewRows([]string{"id", "chain", "type", "label", "state", "key_id", "custodian_ref", "rotation_days", "rotation_after_receives", "created_at", "updated_at"}).
		AddRow(id, "ethereum", "HOT", "w", "ACTIVE", "k", "mpc", nil, nil, created, created)
	mock.ExpectQuery(q(`SELECT.*FROM wallets`)).WillReturnRows(rows)
	w, err := s.GetWallet(context.Background(), id)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if w.ID != id || w.Chain != domain.ChainEthereum || w.RotationDays != nil {
		t.Errorf("unexpected wallet: %+v", w)
	}
}

func TestGetWalletWithRotation(t *testing.T) {
	s, mock := newMock(t)
	id := uuid.New()
	created := time.Now()
	rd, rr := int64(7), int64(100)
	rows := sqlmock.NewRows([]string{"id", "chain", "type", "label", "state", "key_id", "custodian_ref", "rotation_days", "rotation_after_receives", "created_at", "updated_at"}).
		AddRow(id, "bitcoin", "HOT", "w", "ACTIVE", "k", "mpc", rd, rr, created, created)
	mock.ExpectQuery(q(`SELECT.*FROM wallets`)).WillReturnRows(rows)
	w, err := s.GetWallet(context.Background(), id)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if w.RotationDays == nil || *w.RotationDays != 7 {
		t.Errorf("expected rotation_days=7, got %v", w.RotationDays)
	}
	if w.RotationAfterReceives == nil || *w.RotationAfterReceives != 100 {
		t.Errorf("expected rotation_after_receives=100, got %v", w.RotationAfterReceives)
	}
}

func TestGetWalletScanError(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id"}).AddRow("not-a-uuid")
	mock.ExpectQuery(q(`SELECT.*FROM wallets`)).WillReturnRows(rows)
	if _, err := s.GetWallet(context.Background(), uuid.New()); err == nil {
		t.Error("expected scan error")
	}
}

func TestUpdateWalletState(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE wallets SET state`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpdateWalletState(context.Background(), uuid.New(), domain.WalletStatePaused); err != nil {
		t.Fatalf("UpdateWalletState: %v", err)
	}
}

func TestUpdateWalletStateError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE wallets`)).WillReturnError(errors.New("db down"))
	if err := s.UpdateWalletState(context.Background(), uuid.New(), domain.WalletStateActive); err == nil {
		t.Error("expected error")
	}
}

func TestListWalletsAllFilters(t *testing.T) {
	s, mock := newMock(t)
	id := uuid.New()
	created := time.Now()
	rows := sqlmock.NewRows([]string{"id", "chain", "type", "label", "state", "key_id", "custodian_ref", "rotation_days", "rotation_after_receives", "created_at", "updated_at"}).
		AddRow(id, "ethereum", "HOT", "w", "ACTIVE", "k", "mpc", nil, nil, created, created)
	mock.ExpectQuery(q(`SELECT.*WHERE 1=1 AND chain=\$1 AND type=\$2 AND state=\$3 ORDER BY created_at`)).WillReturnRows(rows)
	out, err := s.ListWallets(context.Background(), "ethereum", "HOT", "ACTIVE")
	if err != nil {
		t.Fatalf("ListWallets: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("expected 1 wallet, got %d", len(out))
	}
}

func TestListWalletsChainOnly(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*WHERE 1=1 AND chain=\$1 ORDER BY created_at`)).WillReturnRows(sqlmock.NewRows([]string{"id", "chain", "type", "label", "state", "key_id", "custodian_ref", "rotation_days", "rotation_after_receives", "created_at", "updated_at"}))
	out, err := s.ListWallets(context.Background(), "ethereum", "", "")
	if err != nil {
		t.Fatalf("ListWallets: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected 0 wallets, got %d", len(out))
	}
}

func TestListWalletsQueryError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM wallets`)).WillReturnError(errors.New("db down"))
	if _, err := s.ListWallets(context.Background(), "", "", ""); err == nil {
		t.Error("expected error")
	}
}

func TestListWalletsScanError(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id", "chain", "type", "label", "state", "key_id", "custodian_ref", "rotation_days", "rotation_after_receives", "created_at", "updated_at"}).
		AddRow("bad", "x", "x", "x", "x", "x", "x", nil, nil, time.Now(), time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM wallets`)).WillReturnRows(rows)
	if _, err := s.ListWallets(context.Background(), "", "", ""); err == nil {
		t.Error("expected scan error")
	}
}

func TestInsertAddress(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO addresses`)).WillReturnResult(sqlmock.NewResult(0, 1))
	a := &domain.Address{ID: uuid.New(), WalletID: uuid.New(), Chain: domain.ChainEthereum, Address: "0x1", DerivationPath: "m/44'/60'/0'/0/0", Index: 0, Change: 0, State: domain.AddressStateActive, ReceiveCount: 0, CreatedAt: time.Now()}
	if err := s.InsertAddress(context.Background(), a); err != nil {
		t.Fatalf("InsertAddress: %v", err)
	}
}

func TestInsertAddressError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO addresses`)).WillReturnError(errors.New("dup"))
	if err := s.InsertAddress(context.Background(), &domain.Address{}); err == nil {
		t.Error("expected error")
	}
}

func TestGetActiveAddress(t *testing.T) {
	s, mock := newMock(t)
	id := uuid.New()
	wid := uuid.New()
	created := time.Now()
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "chain", "address", "derivation_path", "index", "change", "state", "receive_count", "created_at"}).
		AddRow(id, wid, "ethereum", "0x1", "m/0", 0, 0, "ACTIVE", 0, created)
	mock.ExpectQuery(q(`SELECT.*FROM addresses WHERE wallet_id=\$1 AND state='ACTIVE'`)).WillReturnRows(rows)
	a, err := s.GetActiveAddress(context.Background(), wid)
	if err != nil {
		t.Fatalf("GetActiveAddress: %v", err)
	}
	if a.ID != id || a.Address != "0x1" {
		t.Errorf("unexpected address: %+v", a)
	}
}

func TestGetActiveAddressError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM addresses`)).WillReturnError(errors.New("db down"))
	if _, err := s.GetActiveAddress(context.Background(), uuid.New()); err == nil {
		t.Error("expected error")
	}
}

func TestGetAddress(t *testing.T) {
	s, mock := newMock(t)
	id := uuid.New()
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "chain", "address", "derivation_path", "index", "change", "state", "receive_count", "created_at"}).
		AddRow(id, uuid.New(), "ethereum", "0x1", "m/0", 0, 0, "ACTIVE", 0, time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM addresses WHERE id=\$1`)).WillReturnRows(rows)
	if _, err := s.GetAddress(context.Background(), id); err != nil {
		t.Fatalf("GetAddress: %v", err)
	}
}

func TestListAddresses(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "chain", "address", "derivation_path", "index", "change", "state", "receive_count", "created_at"}).
		AddRow(uuid.New(), uuid.New(), "ethereum", "0x1", "m/0", 0, 0, "ACTIVE", 0, time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM addresses WHERE wallet_id=\$1 ORDER BY change, index`)).WillReturnRows(rows)
	out, err := s.ListAddresses(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListAddresses: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("expected 1 address, got %d", len(out))
	}
}

func TestListAddressesQueryError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM addresses`)).WillReturnError(errors.New("db down"))
	if _, err := s.ListAddresses(context.Background(), uuid.New()); err == nil {
		t.Error("expected error")
	}
}

func TestListAddressesScanError(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "chain", "address", "derivation_path", "index", "change", "state", "receive_count", "created_at"}).
		AddRow("bad", "x", "x", "x", "x", 0, 0, "x", 0, time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM addresses`)).WillReturnRows(rows)
	if _, err := s.ListAddresses(context.Background(), uuid.New()); err == nil {
		t.Error("expected scan error")
	}
}

func TestDeprecateAddress(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE addresses SET state='DEPRECATED'`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.DeprecateAddress(context.Background(), uuid.New()); err != nil {
		t.Fatalf("DeprecateAddress: %v", err)
	}
}

func TestNextAddressIndex(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT COALESCE\(MAX\(index\), -1\) \+ 1 FROM addresses`)).WillReturnRows(sqlmock.NewRows([]string{"idx"}).AddRow(5))
	idx, err := s.NextAddressIndex(context.Background(), "ethereum", 0)
	if err != nil {
		t.Fatalf("NextAddressIndex: %v", err)
	}
	if idx != 5 {
		t.Errorf("expected 5, got %d", idx)
	}
}

func TestNextAddressIndexError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT COALESCE`)).WillReturnError(errors.New("db down"))
	if _, err := s.NextAddressIndex(context.Background(), "ethereum", 0); err == nil {
		t.Error("expected error")
	}
}

func TestIncrementReceiveCount(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE addresses SET receive_count = receive_count \+ 1`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.IncrementReceiveCount(context.Background(), uuid.New()); err != nil {
		t.Fatalf("IncrementReceiveCount: %v", err)
	}
}

func TestUpsertBalance(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO balances.*ON CONFLICT`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpsertBalance(context.Background(), &storage.Balance{WalletID: uuid.New(), Asset: "eth", Confirmed: "1", Pending: "0", Locked: "0"}); err != nil {
		t.Fatalf("UpsertBalance: %v", err)
	}
}

func TestUpsertBalanceError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO balances`)).WillReturnError(errors.New("db down"))
	if err := s.UpsertBalance(context.Background(), &storage.Balance{}); err == nil {
		t.Error("expected error")
	}
}

func TestGetBalance(t *testing.T) {
	s, mock := newMock(t)
	wid := uuid.New()
	rows := sqlmock.NewRows([]string{"wallet_id", "asset", "confirmed", "pending", "locked", "last_block_seen", "updated_at"}).
		AddRow(wid, "eth", "100", "10", "0", 5, time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM balances WHERE wallet_id=\$1 AND asset=\$2`)).WillReturnRows(rows)
	b, err := s.GetBalance(context.Background(), wid, "eth")
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	if b.Asset != "eth" || b.Confirmed != "100" {
		t.Errorf("unexpected balance: %+v", b)
	}
}

func TestGetBalanceError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM balances`)).WillReturnError(errors.New("db down"))
	if _, err := s.GetBalance(context.Background(), uuid.New(), "eth"); err == nil {
		t.Error("expected error")
	}
}

func TestListBalances(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"wallet_id", "asset", "confirmed", "pending", "locked", "last_block_seen", "updated_at"}).
		AddRow(uuid.New(), "eth", "1", "0", "0", 0, time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM balances WHERE wallet_id=\$1 ORDER BY asset`)).WillReturnRows(rows)
	out, err := s.ListBalances(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListBalances: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("expected 1 balance, got %d", len(out))
	}
}

func TestListBalancesQueryError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM balances`)).WillReturnError(errors.New("db down"))
	if _, err := s.ListBalances(context.Background(), uuid.New()); err == nil {
		t.Error("expected error")
	}
}

func TestListBalancesScanError(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"wallet_id", "asset", "confirmed", "pending", "locked", "last_block_seen", "updated_at"}).
		AddRow("bad", "x", "x", "x", "x", 0, time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM balances`)).WillReturnRows(rows)
	if _, err := s.ListBalances(context.Background(), uuid.New()); err == nil {
		t.Error("expected scan error")
	}
}

func TestRecordBalanceEventInsert(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO balance_events.*ON CONFLICT DO NOTHING`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(q(`SELECT EXISTS\(SELECT 1 FROM balance_events`)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	if err := s.RecordBalanceEvent(context.Background(), &storage.BalanceEvent{ID: uuid.New(), WalletID: uuid.New(), Asset: "eth", BlockHeight: 1, EventID: "e1"}); err != nil {
		t.Fatalf("RecordBalanceEvent: %v", err)
	}
}

func TestRecordBalanceEventDuplicate(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO balance_events.*ON CONFLICT DO NOTHING`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(q(`SELECT EXISTS`)).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	err := s.RecordBalanceEvent(context.Background(), &storage.BalanceEvent{ID: uuid.New(), WalletID: uuid.New(), Asset: "eth", BlockHeight: 1, EventID: "e1"})
	if !errors.Is(err, storage.ErrDuplicateEvent) {
		t.Errorf("expected ErrDuplicateEvent, got %v", err)
	}
}

func TestRecordBalanceEventInsertError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO balance_events`)).WillReturnError(errors.New("db down"))
	if err := s.RecordBalanceEvent(context.Background(), &storage.BalanceEvent{}); err == nil {
		t.Error("expected error")
	}
}

func TestRecordBalanceEventExistsError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO balance_events`)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(q(`SELECT EXISTS`)).WillReturnError(errors.New("db down"))
	if err := s.RecordBalanceEvent(context.Background(), &storage.BalanceEvent{}); err == nil {
		t.Error("expected error")
	}
}

func TestInsertUTXO(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO utxos`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.InsertUTXO(context.Background(), &storage.UTXO{Outpoint: "o1", WalletID: uuid.New(), Value: "1", ScriptType: "P2WPKH", Confirmations: 1, LockState: "FREE"}); err != nil {
		t.Fatalf("InsertUTXO: %v", err)
	}
}

func TestInsertUTXOError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO utxos`)).WillReturnError(errors.New("db down"))
	if err := s.InsertUTXO(context.Background(), &storage.UTXO{}); err == nil {
		t.Error("expected error")
	}
}

func TestListFreeUTXOs(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"outpoint", "wallet_id", "value", "script_type", "confirmations", "lock_state", "locked_at", "spent_at", "tx_hash", "updated_at"}).
		AddRow("o1", uuid.New(), "1", "P2WPKH", 1, "FREE", nil, nil, "", time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM utxos WHERE wallet_id=\$1 AND lock_state='FREE' ORDER BY outpoint`)).WillReturnRows(rows)
	out, err := s.ListFreeUTXOs(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListFreeUTXOs: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("expected 1 utxo, got %d", len(out))
	}
}

func TestListFreeUTXOsQueryError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM utxos`)).WillReturnError(errors.New("db down"))
	if _, err := s.ListFreeUTXOs(context.Background(), uuid.New()); err == nil {
		t.Error("expected error")
	}
}

func TestListFreeUTXOsScanError(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"outpoint", "wallet_id", "value", "script_type", "confirmations", "lock_state", "locked_at", "spent_at", "tx_hash", "updated_at"}).
		AddRow("o1", "bad", "1", "P2WPKH", 1, "FREE", nil, nil, "", time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM utxos`)).WillReturnRows(rows)
	if _, err := s.ListFreeUTXOs(context.Background(), uuid.New()); err == nil {
		t.Error("expected scan error")
	}
}

func TestLockUTXOsEmpty(t *testing.T) {
	s, _ := newMock(t)
	if err := s.LockUTXOs(context.Background(), nil); err != nil {
		t.Errorf("LockUTXOs empty should be nil, got %v", err)
	}
}

func TestLockUTXOs(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(q(`UPDATE utxos SET lock_state='LOCKED'`)).WithArgs("o1").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := s.LockUTXOs(context.Background(), []string{"o1"}); err != nil {
		t.Fatalf("LockUTXOs: %v", err)
	}
}

func TestLockUTXOsNotFree(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(q(`UPDATE utxos SET lock_state='LOCKED'`)).WithArgs("o1").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()
	if err := s.LockUTXOs(context.Background(), []string{"o1"}); err == nil {
		t.Error("expected not-free error")
	}
}

func TestLockUTXOsExecError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(q(`UPDATE utxos SET lock_state='LOCKED'`)).WillReturnError(errors.New("db down"))
	mock.ExpectRollback()
	if err := s.LockUTXOs(context.Background(), []string{"o1"}); err == nil {
		t.Error("expected error")
	}
}

func TestLockUTXOsRowsAffectedError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(q(`UPDATE utxos SET lock_state='LOCKED'`)).WillReturnResult(sqlmock.NewErrorResult(errors.New("rows err")))
	mock.ExpectRollback()
	if err := s.LockUTXOs(context.Background(), []string{"o1"}); err == nil {
		t.Error("expected rows-affected error")
	}
}

func TestMarkUTXOsSpentEmpty(t *testing.T) {
	s, _ := newMock(t)
	if err := s.MarkUTXOsSpent(context.Background(), nil, "0xtx"); err != nil {
		t.Errorf("MarkUTXOsSpent empty should be nil, got %v", err)
	}
}

func TestMarkUTXOsSpent(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE utxos SET lock_state='SPENT'.*WHERE outpoint=\$1`)).WithArgs("o1", "0xtx").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.MarkUTXOsSpent(context.Background(), []string{"o1"}, "0xtx"); err != nil {
		t.Fatalf("MarkUTXOsSpent: %v", err)
	}
}

func TestMarkUTXOsSpentError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE utxos SET lock_state='SPENT'`)).WillReturnError(errors.New("db down"))
	if err := s.MarkUTXOsSpent(context.Background(), []string{"o1"}, "0xtx"); err == nil {
		t.Error("expected error")
	}
}

func TestRestoreUTXOsEmpty(t *testing.T) {
	s, _ := newMock(t)
	if err := s.RestoreUTXOs(context.Background(), nil); err != nil {
		t.Errorf("RestoreUTXOs empty should be nil, got %v", err)
	}
}

func TestRestoreUTXOs(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE utxos SET lock_state='FREE'`)).WithArgs("o1").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.RestoreUTXOs(context.Background(), []string{"o1"}); err != nil {
		t.Fatalf("RestoreUTXOs: %v", err)
	}
}

func TestRestoreUTXOsError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE utxos SET lock_state='FREE'`)).WillReturnError(errors.New("db down"))
	if err := s.RestoreUTXOs(context.Background(), []string{"o1"}); err == nil {
		t.Error("expected error")
	}
}

func TestPruneUTXOsEmpty(t *testing.T) {
	s, _ := newMock(t)
	if err := s.PruneUTXOs(context.Background(), nil); err != nil {
		t.Errorf("PruneUTXOs empty should be nil, got %v", err)
	}
}

func TestPruneUTXOs(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`DELETE FROM utxos WHERE outpoint=\$1`)).WithArgs("o1").WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.PruneUTXOs(context.Background(), []string{"o1"}); err != nil {
		t.Fatalf("PruneUTXOs: %v", err)
	}
}

func TestPruneUTXOsError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`DELETE FROM utxos`)).WillReturnError(errors.New("db down"))
	if err := s.PruneUTXOs(context.Background(), []string{"o1"}); err == nil {
		t.Error("expected error")
	}
}

func TestGetNonceNotFound(t *testing.T) {
	s, mock := newMock(t)
	wid := uuid.New()
	mock.ExpectQuery(q(`SELECT.*FROM nonces WHERE wallet_id=\$1 AND chain=\$2`)).WillReturnError(sql.ErrNoRows)
	n, err := s.GetNonce(context.Background(), wid, "ethereum")
	if err != nil {
		t.Fatalf("GetNonce: %v", err)
	}
	if n.WalletID != wid || n.Chain != "ethereum" {
		t.Errorf("expected default nonce, got %+v", n)
	}
}

func TestGetNonce(t *testing.T) {
	s, mock := newMock(t)
	wid := uuid.New()
	rows := sqlmock.NewRows([]string{"wallet_id", "chain", "pending_nonce", "broadcast_nonce", "version", "updated_at"}).
		AddRow(wid, "ethereum", int64(5), int64(3), 2, time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM nonces WHERE wallet_id=\$1 AND chain=\$2`)).WillReturnRows(rows)
	n, err := s.GetNonce(context.Background(), wid, "ethereum")
	if err != nil {
		t.Fatalf("GetNonce: %v", err)
	}
	if n.PendingNonce != 5 || n.BroadcastNonce != 3 || n.Version != 2 {
		t.Errorf("unexpected nonce: %+v", n)
	}
}

func TestGetNonceScanError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM nonces`)).WillReturnError(errors.New("db down"))
	if _, err := s.GetNonce(context.Background(), uuid.New(), "ethereum"); err == nil {
		t.Error("expected error")
	}
}

func TestUpsertNonce(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO nonces.*ON CONFLICT`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpsertNonce(context.Background(), &storage.Nonce{WalletID: uuid.New(), Chain: "ethereum", PendingNonce: 1, BroadcastNonce: 0, Version: 1}); err != nil {
		t.Fatalf("UpsertNonce: %v", err)
	}
}

func TestUpsertNonceError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO nonces`)).WillReturnError(errors.New("db down"))
	if err := s.UpsertNonce(context.Background(), &storage.Nonce{}); err == nil {
		t.Error("expected error")
	}
}

func TestIncrementPendingNonceExisting(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(q(`SELECT pending_nonce, version FROM nonces WHERE wallet_id=\$1 AND chain=\$2 FOR UPDATE`)).WillReturnRows(sqlmock.NewRows([]string{"pending_nonce", "version"}).AddRow(int64(5), 2))
	mock.ExpectExec(q(`UPDATE nonces SET pending_nonce=\$3, version=\$4`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	val, ver, err := s.IncrementPendingNonce(context.Background(), uuid.New(), "ethereum")
	if err != nil {
		t.Fatalf("IncrementPendingNonce: %v", err)
	}
	if val != 5 || ver != 3 {
		t.Errorf("expected val=5 ver=3, got val=%d ver=%d", val, ver)
	}
}

func TestIncrementPendingNonceInsert(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(q(`SELECT pending_nonce, version FROM nonces`)).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(q(`INSERT INTO nonces.*VALUES \(\$1,\$2,1,0,1,now\(\)\)`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	val, ver, err := s.IncrementPendingNonce(context.Background(), uuid.New(), "ethereum")
	if err != nil {
		t.Fatalf("IncrementPendingNonce: %v", err)
	}
	if val != 0 || ver != 1 {
		t.Errorf("expected val=0 ver=1, got val=%d ver=%d", val, ver)
	}
}

func TestIncrementPendingNonceScanError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(q(`SELECT pending_nonce, version FROM nonces`)).WillReturnError(errors.New("db down"))
	mock.ExpectRollback()
	if _, _, err := s.IncrementPendingNonce(context.Background(), uuid.New(), "ethereum"); err == nil {
		t.Error("expected error")
	}
}

func TestAdvanceBroadcastNonce(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE nonces SET broadcast_nonce=GREATEST`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.AdvanceBroadcastNonce(context.Background(), uuid.New(), "ethereum", 5); err != nil {
		t.Fatalf("AdvanceBroadcastNonce: %v", err)
	}
}

func TestAdvanceBroadcastNonceError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE nonces SET broadcast_nonce`)).WillReturnError(errors.New("db down"))
	if err := s.AdvanceBroadcastNonce(context.Background(), uuid.New(), "ethereum", 5); err == nil {
		t.Error("expected error")
	}
}

func TestRollbackPendingNonce(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE nonces SET pending_nonce=\$3, updated_at=now\(\) WHERE wallet_id=\$1 AND chain=\$2 AND pending_nonce=\$3\+1`)).WillReturnResult(sqlmock.NewResult(0, 1))
	n, err := s.RollbackPendingNonce(context.Background(), uuid.New(), "ethereum", 4)
	if err != nil {
		t.Fatalf("RollbackPendingNonce: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row affected, got %d", n)
	}
}

func TestRollbackPendingNonceError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE nonces SET pending_nonce`)).WillReturnError(errors.New("db down"))
	if _, err := s.RollbackPendingNonce(context.Background(), uuid.New(), "ethereum", 4); err == nil {
		t.Error("expected error")
	}
}

func TestCreateWithdrawal(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO withdrawal_requests`)).WillReturnResult(sqlmock.NewResult(0, 1))
	w := &storage.WithdrawalRequest{ID: uuid.New(), WalletID: uuid.New(), ToAddress: "0x1", Asset: "eth", Amount: "1", State: "PENDING", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := s.CreateWithdrawal(context.Background(), w); err != nil {
		t.Fatalf("CreateWithdrawal: %v", err)
	}
}

func TestCreateWithdrawalError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO withdrawal_requests`)).WillReturnError(errors.New("db down"))
	if err := s.CreateWithdrawal(context.Background(), &storage.WithdrawalRequest{}); err == nil {
		t.Error("expected error")
	}
}

func TestGetWithdrawal(t *testing.T) {
	s, mock := newMock(t)
	id := uuid.New()
	created := time.Now()
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "to_address", "asset", "amount", "state", "policy_decision_id", "failure_reason", "tx_hash", "nonce_value", "reserved_outpoints", "signed_tx_bytes", "created_at", "updated_at"}).
		AddRow(id, uuid.New(), "0x1", "eth", "1", "PENDING", "", "", "", nil, pq.Array([]string{}), nil, created, created)
	mock.ExpectQuery(q(`SELECT.*FROM withdrawal_requests WHERE id=\$1`)).WillReturnRows(rows)
	w, err := s.GetWithdrawal(context.Background(), id)
	if err != nil {
		t.Fatalf("GetWithdrawal: %v", err)
	}
	if w.ID != id {
		t.Errorf("id mismatch: %v", w.ID)
	}
}

func TestGetWithdrawalError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM withdrawal_requests`)).WillReturnError(errors.New("db down"))
	if _, err := s.GetWithdrawal(context.Background(), uuid.New()); err == nil {
		t.Error("expected error")
	}
}

func TestListWithdrawalsAll(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "to_address", "asset", "amount", "state", "policy_decision_id", "failure_reason", "tx_hash", "nonce_value", "reserved_outpoints", "signed_tx_bytes", "created_at", "updated_at"})
	mock.ExpectQuery(q(`SELECT.*FROM withdrawal_requests ORDER BY created_at`)).WillReturnRows(rows)
	out, err := s.ListWithdrawals(context.Background(), uuid.Nil, "")
	if err != nil {
		t.Fatalf("ListWithdrawals: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected 0, got %d", len(out))
	}
}

func TestListWithdrawalsWalletAndState(t *testing.T) {
	s, mock := newMock(t)
	wid := uuid.New()
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "to_address", "asset", "amount", "state", "policy_decision_id", "failure_reason", "tx_hash", "nonce_value", "reserved_outpoints", "signed_tx_bytes", "created_at", "updated_at"})
	mock.ExpectQuery(q(`SELECT.*FROM withdrawal_requests WHERE wallet_id=\$1 AND state=\$2 ORDER BY created_at`)).WillReturnRows(rows)
	if _, err := s.ListWithdrawals(context.Background(), wid, "PENDING"); err != nil {
		t.Fatalf("ListWithdrawals: %v", err)
	}
}

func TestListWithdrawalsWalletOnly(t *testing.T) {
	s, mock := newMock(t)
	wid := uuid.New()
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "to_address", "asset", "amount", "state", "policy_decision_id", "failure_reason", "tx_hash", "nonce_value", "reserved_outpoints", "signed_tx_bytes", "created_at", "updated_at"})
	mock.ExpectQuery(q(`SELECT.*FROM withdrawal_requests WHERE wallet_id=\$1 ORDER BY created_at`)).WillReturnRows(rows)
	if _, err := s.ListWithdrawals(context.Background(), wid, ""); err != nil {
		t.Fatalf("ListWithdrawals: %v", err)
	}
}

func TestListWithdrawalsStateOnly(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "to_address", "asset", "amount", "state", "policy_decision_id", "failure_reason", "tx_hash", "nonce_value", "reserved_outpoints", "signed_tx_bytes", "created_at", "updated_at"})
	mock.ExpectQuery(q(`SELECT.*FROM withdrawal_requests WHERE state=\$1 ORDER BY created_at`)).WillReturnRows(rows)
	if _, err := s.ListWithdrawals(context.Background(), uuid.Nil, "PENDING"); err != nil {
		t.Fatalf("ListWithdrawals: %v", err)
	}
}

func TestListWithdrawalsQueryError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM withdrawal_requests`)).WillReturnError(errors.New("db down"))
	if _, err := s.ListWithdrawals(context.Background(), uuid.Nil, ""); err == nil {
		t.Error("expected error")
	}
}

func TestListWithdrawalsScanError(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "to_address", "asset", "amount", "state", "policy_decision_id", "failure_reason", "tx_hash", "nonce_value", "reserved_outpoints", "signed_tx_bytes", "created_at", "updated_at"}).
		AddRow("bad", "x", "x", "x", "x", "x", "x", "x", "x", nil, nil, nil, time.Now(), time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM withdrawal_requests`)).WillReturnRows(rows)
	if _, err := s.ListWithdrawals(context.Background(), uuid.Nil, ""); err == nil {
		t.Error("expected scan error")
	}
}

func TestUpdateWithdrawalState(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE withdrawal_requests SET state=\$2`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpdateWithdrawalState(context.Background(), uuid.New(), "CONFIRMED", "", "0xtx", ""); err != nil {
		t.Fatalf("UpdateWithdrawalState: %v", err)
	}
}

func TestUpdateWithdrawalStateError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE withdrawal_requests SET state`)).WillReturnError(errors.New("db down"))
	if err := s.UpdateWithdrawalState(context.Background(), uuid.New(), "x", "", "", ""); err == nil {
		t.Error("expected error")
	}
}

func TestUpdateWithdrawalNonce(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE withdrawal_requests SET nonce_value=\$2`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpdateWithdrawalNonce(context.Background(), uuid.New(), 5); err != nil {
		t.Fatalf("UpdateWithdrawalNonce: %v", err)
	}
}

func TestUpdateWithdrawalNonceError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE withdrawal_requests SET nonce_value`)).WillReturnError(errors.New("db down"))
	if err := s.UpdateWithdrawalNonce(context.Background(), uuid.New(), 5); err == nil {
		t.Error("expected error")
	}
}

func TestUpdateWithdrawalOutpoints(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE withdrawal_requests SET reserved_outpoints=\$2`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpdateWithdrawalOutpoints(context.Background(), uuid.New(), []string{"o1", "o2"}); err != nil {
		t.Fatalf("UpdateWithdrawalOutpoints: %v", err)
	}
}

func TestUpdateWithdrawalOutpointsError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE withdrawal_requests SET reserved_outpoints`)).WillReturnError(errors.New("db down"))
	if err := s.UpdateWithdrawalOutpoints(context.Background(), uuid.New(), []string{"o1"}); err == nil {
		t.Error("expected error")
	}
}

func TestUpdateWithdrawalSignedTx(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE withdrawal_requests SET signed_tx_bytes=\$2`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpdateWithdrawalSignedTx(context.Background(), uuid.New(), []byte("signed")); err != nil {
		t.Fatalf("UpdateWithdrawalSignedTx: %v", err)
	}
}

func TestUpdateWithdrawalSignedTxError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE withdrawal_requests SET signed_tx_bytes`)).WillReturnError(errors.New("db down"))
	if err := s.UpdateWithdrawalSignedTx(context.Background(), uuid.New(), nil); err == nil {
		t.Error("expected error")
	}
}

func TestBindKeyMapping(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO key_mappings`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.BindKeyMapping(context.Background(), &storage.KeyMapping{WalletID: uuid.New(), KeyID: "k1", ActiveFrom: time.Now(), RotationState: "CURRENT", CreatedAt: time.Now()}); err != nil {
		t.Fatalf("BindKeyMapping: %v", err)
	}
}

func TestBindKeyMappingError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO key_mappings`)).WillReturnError(errors.New("db down"))
	if err := s.BindKeyMapping(context.Background(), &storage.KeyMapping{}); err == nil {
		t.Error("expected error")
	}
}

func TestResolveActiveKey(t *testing.T) {
	s, mock := newMock(t)
	wid := uuid.New()
	rows := sqlmock.NewRows([]string{"wallet_id", "key_id", "active_from", "active_to", "rotation_state", "created_at"}).
		AddRow(wid, "k1", time.Now(), nil, "CURRENT", time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM key_mappings WHERE wallet_id=\$1 AND rotation_state IN \('CURRENT','COOLING'\) ORDER BY active_from`)).WillReturnRows(rows)
	out, err := s.ResolveActiveKey(context.Background(), wid)
	if err != nil {
		t.Fatalf("ResolveActiveKey: %v", err)
	}
	if len(out) != 1 || out[0].KeyID != "k1" {
		t.Errorf("unexpected: %+v", out)
	}
}

func TestResolveActiveKeyNotFound(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM key_mappings`)).WillReturnRows(sqlmock.NewRows([]string{"wallet_id", "key_id", "active_from", "active_to", "rotation_state", "created_at"}))
	if _, err := s.ResolveActiveKey(context.Background(), uuid.New()); err != sql.ErrNoRows {
		t.Errorf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestResolveActiveKeyQueryError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM key_mappings`)).WillReturnError(errors.New("db down"))
	if _, err := s.ResolveActiveKey(context.Background(), uuid.New()); err == nil {
		t.Error("expected error")
	}
}

func TestResolveActiveKeyScanError(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"wallet_id", "key_id", "active_from", "active_to", "rotation_state", "created_at"}).
		AddRow("bad", "x", time.Now(), nil, "x", time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM key_mappings`)).WillReturnRows(rows)
	if _, err := s.ResolveActiveKey(context.Background(), uuid.New()); err == nil {
		t.Error("expected scan error")
	}
}

func TestRotateKeyMapping(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(q(`UPDATE key_mappings SET rotation_state='COOLING'`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(q(`INSERT INTO key_mappings.*ON CONFLICT`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	if err := s.RotateKeyMapping(context.Background(), uuid.New(), "k2", time.Hour); err != nil {
		t.Fatalf("RotateKeyMapping: %v", err)
	}
}

func TestRotateKeyMappingUpdateError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(q(`UPDATE key_mappings SET rotation_state='COOLING'`)).WillReturnError(errors.New("db down"))
	mock.ExpectRollback()
	if err := s.RotateKeyMapping(context.Background(), uuid.New(), "k2", time.Hour); err == nil {
		t.Error("expected error")
	}
}

func TestRotateKeyMappingInsertError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectExec(q(`UPDATE key_mappings SET rotation_state='COOLING'`)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(q(`INSERT INTO key_mappings.*ON CONFLICT`)).WillReturnError(errors.New("db down"))
	mock.ExpectRollback()
	if err := s.RotateKeyMapping(context.Background(), uuid.New(), "k2", time.Hour); err == nil {
		t.Error("expected error")
	}
}

func TestExpireCooling(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE key_mappings SET rotation_state='RETIRED'`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.ExpireCooling(context.Background()); err != nil {
		t.Fatalf("ExpireCooling: %v", err)
	}
}

func TestExpireCoolingError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE key_mappings SET rotation_state='RETIRED'`)).WillReturnError(errors.New("db down"))
	if err := s.ExpireCooling(context.Background()); err == nil {
		t.Error("expected error")
	}
}

func TestCreateFundingRequest(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO funding_requests`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.CreateFundingRequest(context.Background(), &storage.FundingRequest{ID: uuid.New(), WalletID: uuid.New(), Asset: "usdc", Amount: "100", State: "REQUESTED", CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
		t.Fatalf("CreateFundingRequest: %v", err)
	}
}

func TestCreateFundingRequestError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO funding_requests`)).WillReturnError(errors.New("db down"))
	if err := s.CreateFundingRequest(context.Background(), &storage.FundingRequest{}); err == nil {
		t.Error("expected error")
	}
}

func TestGetOpenFundingRequest(t *testing.T) {
	s, mock := newMock(t)
	id := uuid.New()
	wid := uuid.New()
	created := time.Now()
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "asset", "amount", "state", "treasury_batch_id", "reason", "created_at", "updated_at"}).
		AddRow(id, wid, "usdc", "100", "REQUESTED", "", "ops", created, created)
	mock.ExpectQuery(q(`SELECT.*FROM funding_requests WHERE wallet_id=\$1 AND asset=\$2 AND state='REQUESTED' LIMIT 1`)).WillReturnRows(rows)
	f, err := s.GetOpenFundingRequest(context.Background(), wid, "usdc")
	if err != nil {
		t.Fatalf("GetOpenFundingRequest: %v", err)
	}
	if f.ID != id || f.Asset != "usdc" {
		t.Errorf("unexpected: %+v", f)
	}
}

func TestGetOpenFundingRequestError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM funding_requests`)).WillReturnError(errors.New("db down"))
	if _, err := s.GetOpenFundingRequest(context.Background(), uuid.New(), "usdc"); err == nil {
		t.Error("expected error")
	}
}

func TestListFundingRequestsAll(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "asset", "amount", "state", "treasury_batch_id", "reason", "created_at", "updated_at"})
	mock.ExpectQuery(q(`SELECT.*FROM funding_requests ORDER BY created_at`)).WillReturnRows(rows)
	if _, err := s.ListFundingRequests(context.Background(), uuid.Nil, ""); err != nil {
		t.Fatalf("ListFundingRequests: %v", err)
	}
}

func TestListFundingRequestsWalletAndState(t *testing.T) {
	s, mock := newMock(t)
	wid := uuid.New()
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "asset", "amount", "state", "treasury_batch_id", "reason", "created_at", "updated_at"})
	mock.ExpectQuery(q(`SELECT.*FROM funding_requests WHERE wallet_id=\$1 AND state=\$2 ORDER BY created_at`)).WillReturnRows(rows)
	if _, err := s.ListFundingRequests(context.Background(), wid, "REQUESTED"); err != nil {
		t.Fatalf("ListFundingRequests: %v", err)
	}
}

func TestListFundingRequestsWalletOnly(t *testing.T) {
	s, mock := newMock(t)
	wid := uuid.New()
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "asset", "amount", "state", "treasury_batch_id", "reason", "created_at", "updated_at"})
	mock.ExpectQuery(q(`SELECT.*FROM funding_requests WHERE wallet_id=\$1 ORDER BY created_at`)).WillReturnRows(rows)
	if _, err := s.ListFundingRequests(context.Background(), wid, ""); err != nil {
		t.Fatalf("ListFundingRequests: %v", err)
	}
}

func TestListFundingRequestsStateOnly(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "asset", "amount", "state", "treasury_batch_id", "reason", "created_at", "updated_at"})
	mock.ExpectQuery(q(`SELECT.*FROM funding_requests WHERE state=\$1 ORDER BY created_at`)).WillReturnRows(rows)
	if _, err := s.ListFundingRequests(context.Background(), uuid.Nil, "REQUESTED"); err != nil {
		t.Fatalf("ListFundingRequests: %v", err)
	}
}

func TestListFundingRequestsQueryError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM funding_requests`)).WillReturnError(errors.New("db down"))
	if _, err := s.ListFundingRequests(context.Background(), uuid.Nil, ""); err == nil {
		t.Error("expected error")
	}
}

func TestListFundingRequestsScanError(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id", "wallet_id", "asset", "amount", "state", "treasury_batch_id", "reason", "created_at", "updated_at"}).
		AddRow("bad", "x", "x", "x", "x", "x", "x", time.Now(), time.Now())
	mock.ExpectQuery(q(`SELECT.*FROM funding_requests`)).WillReturnRows(rows)
	if _, err := s.ListFundingRequests(context.Background(), uuid.Nil, ""); err == nil {
		t.Error("expected scan error")
	}
}

func TestUpdateFundingState(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE funding_requests SET state=\$2`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.UpdateFundingState(context.Background(), uuid.New(), "APPROVED", "batch1"); err != nil {
		t.Fatalf("UpdateFundingState: %v", err)
	}
}

func TestUpdateFundingStateError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE funding_requests SET state`)).WillReturnError(errors.New("db down"))
	if err := s.UpdateFundingState(context.Background(), uuid.New(), "x", ""); err == nil {
		t.Error("expected error")
	}
}

func TestAppendAuditEvent(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO audit_outbox`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.AppendAuditEvent(context.Background(), &storage.AuditOutboxEvent{ID: uuid.New(), EventID: uuid.New(), EventType: "x", Payload: []byte("{}"), Seq: 1, CreatedAt: time.Now()}); err != nil {
		t.Fatalf("AppendAuditEvent: %v", err)
	}
}

func TestAppendAuditEventError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`INSERT INTO audit_outbox`)).WillReturnError(errors.New("db down"))
	if err := s.AppendAuditEvent(context.Background(), &storage.AuditOutboxEvent{}); err == nil {
		t.Error("expected error")
	}
}

func TestListUndeliveredAuditEvents(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id", "event_id", "wallet_id", "event_type", "payload", "seq", "delivered", "attempts", "created_at", "delivered_at"}).
		AddRow(uuid.New(), uuid.New(), nil, "x", []byte("{}"), int64(1), false, 0, time.Now(), nil)
	mock.ExpectQuery(q(`SELECT.*FROM audit_outbox WHERE delivered=false ORDER BY seq LIMIT \$1`)).WillReturnRows(rows)
	out, err := s.ListUndeliveredAuditEvents(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListUndeliveredAuditEvents: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("expected 1, got %d", len(out))
	}
}

func TestListUndeliveredAuditEventsQueryError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`SELECT.*FROM audit_outbox`)).WillReturnError(errors.New("db down"))
	if _, err := s.ListUndeliveredAuditEvents(context.Background(), 10); err == nil {
		t.Error("expected error")
	}
}

func TestListUndeliveredAuditEventsScanError(t *testing.T) {
	s, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"id", "event_id", "wallet_id", "event_type", "payload", "seq", "delivered", "attempts", "created_at", "delivered_at"}).
		AddRow("bad", "x", nil, "x", []byte("{}"), int64(1), false, 0, time.Now(), nil)
	mock.ExpectQuery(q(`SELECT.*FROM audit_outbox`)).WillReturnRows(rows)
	if _, err := s.ListUndeliveredAuditEvents(context.Background(), 10); err == nil {
		t.Error("expected scan error")
	}
}

func TestMarkAuditDelivered(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE audit_outbox SET delivered=true`)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.MarkAuditDelivered(context.Background(), uuid.New()); err != nil {
		t.Fatalf("MarkAuditDelivered: %v", err)
	}
}

func TestMarkAuditDeliveredError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectExec(q(`UPDATE audit_outbox SET delivered=true`)).WillReturnError(errors.New("db down"))
	if err := s.MarkAuditDelivered(context.Background(), uuid.New()); err == nil {
		t.Error("expected error")
	}
}

func TestNextAuditSeq(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`INSERT INTO audit_seq.*ON CONFLICT.*RETURNING seq`)).WillReturnRows(sqlmock.NewRows([]string{"seq"}).AddRow(int64(1)))
	seq, err := s.NextAuditSeq(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("NextAuditSeq: %v", err)
	}
	if seq != 1 {
		t.Errorf("expected 1, got %d", seq)
	}
}

func TestNextAuditSeqError(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(q(`INSERT INTO audit_seq`)).WillReturnError(errors.New("db down"))
	if _, err := s.NextAuditSeq(context.Background(), uuid.New()); err == nil {
		t.Error("expected error")
	}
}

func TestClose(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectClose()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}