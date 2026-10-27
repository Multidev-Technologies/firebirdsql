package firebirdsql

import (
	"context"
	"database/sql"
	"testing"
)

func TestLiveDescribeStatement(t *testing.T) {
	ctx := context.Background()
	db := firebird25DB(t, "&explicit_tx_only=true")
	db.SetMaxOpenConns(1)
	setup, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.ExecContext(ctx, "recreate table describe_probe (id integer not null primary key, name varchar(20))"); err != nil {
		_ = setup.Rollback()
		t.Fatal(err)
	}
	if err := setup.Commit(); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	got, err := DescribeStatement(ctx, conn, "select id as x, name, id + 1 as n from describe_probe where id = ?")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "select" || !got.ReturnsRows || got.Mutating || got.InputCount != 1 {
		t.Fatalf("descriptor = %+v", got)
	}
	want := []ColumnOrigin{{"DESCRIBE_PROBE", "ID"}, {"DESCRIBE_PROBE", "NAME"}, {}}
	if len(got.Columns) != len(want) {
		t.Fatalf("column origins = %+v, want %+v", got.Columns, want)
	}
	for i := range want {
		if got.Columns[i] != want[i] {
			t.Fatalf("column %d origin = %+v, want %+v", i, got.Columns[i], want[i])
		}
	}

	// Inside an open read-only transaction: reuse it and leave it open.
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DescribeStatement(ctx, conn, "delete from describe_probe"); err != nil {
		t.Fatal(err)
	}
	var one int
	if err := tx.QueryRowContext(ctx, "select 1 from rdb$database").Scan(&one); err != nil {
		t.Fatalf("transaction closed by describe: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	// A prepare error must end the helper's transaction and restore the
	// connection's explicit-transaction state.
	if _, err := DescribeStatement(ctx, conn, "select from describe_probe"); err == nil {
		t.Fatal("describing invalid SQL succeeded")
	}
	tx, err = conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("begin after describe error: %v", err)
	}
	defer tx.Rollback()
	var active int
	if err := tx.QueryRowContext(ctx, "select count(*) from mon$transactions where mon$attachment_id = current_connection").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 { // only tx itself
		t.Fatalf("attachment holds %d transactions after describe error, want 1", active)
	}
}
