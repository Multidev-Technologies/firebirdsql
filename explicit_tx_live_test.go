package firebirdsql

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
)

func firebird25DB(t *testing.T, extra string) *sql.DB {
	t.Helper()
	dsn := os.Getenv("FIREBIRD25_TEST_DSN")
	if dsn == "" {
		t.Skip("FIREBIRD25_TEST_DSN is not set")
	}
	db, err := sql.Open("firebirdsql", dsn+extra)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestLiveExplicitTxOnlyAfterCommit(t *testing.T) {
	ctx := context.Background()
	db := firebird25DB(t, "&explicit_tx_only=true")
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var one int
	if err := tx.QueryRowContext(ctx, "select 1 from rdb$database").Scan(&one); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	err = db.QueryRowContext(ctx, "select 1 from rdb$database").Scan(&one)
	if !errors.Is(err, ErrImplicitTransaction) {
		t.Fatalf("implicit statement err = %v, want ErrImplicitTransaction", err)
	}
	tx2, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx2.Rollback()
	var active int
	if err := tx2.QueryRowContext(ctx, "select count(*) from mon$transactions where mon$attachment_id = current_connection").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 { // only tx2 itself
		t.Fatalf("attachment holds %d transactions, want 1", active)
	}
}

func TestLiveReadOnlySnapshotIgnoresLaterCommits(t *testing.T) {
	ctx := context.Background()
	db := firebird25DB(t, "&explicit_tx_only=true")
	setup, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.ExecContext(ctx, "recreate table fork_snapshot_probe (id integer)"); err != nil {
		_ = setup.Rollback()
		t.Fatal(err)
	}
	if err := setup.Commit(); err != nil {
		t.Fatal(err)
	}
	snap, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelSnapshot})
	if err != nil {
		t.Fatal(err)
	}
	defer snap.Rollback()
	var before int
	if err := snap.QueryRowContext(ctx, "select count(*) from fork_snapshot_probe").Scan(&before); err != nil {
		t.Fatal(err)
	}
	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, "insert into fork_snapshot_probe values (1)"); err != nil {
		_ = writer.Rollback()
		t.Fatal(err)
	}
	if err := writer.Commit(); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := snap.QueryRowContext(ctx, "select count(*) from fork_snapshot_probe").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("snapshot saw a later commit: before %d after %d", before, after)
	}
}
