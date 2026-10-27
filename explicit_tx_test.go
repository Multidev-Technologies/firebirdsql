package firebirdsql

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"
)

func TestReadOnlySnapshotTPB(t *testing.T) {
	level, err := txIsolationLevelWith(driver.TxOptions{ReadOnly: true, Isolation: driver.IsolationLevel(sql.LevelSnapshot)}, false)
	if err != nil {
		t.Fatal(err)
	}
	tpb, err := tpbForIsolationLevel(level)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{byte(isc_tpb_version3), byte(isc_tpb_read), byte(isc_tpb_wait), byte(isc_tpb_concurrency)}
	if !bytes.Equal(tpb, want) {
		t.Fatalf("tpb = %v, want %v", tpb, want)
	}
}

func TestTxNoWaitAppliesToEveryLevel(t *testing.T) {
	cases := []driver.TxOptions{
		{ReadOnly: true},
		{ReadOnly: true, Isolation: driver.IsolationLevel(sql.LevelSnapshot)},
		{},
		{Isolation: driver.IsolationLevel(sql.LevelSnapshot)},
	}
	for _, opts := range cases {
		level, err := txIsolationLevelWith(opts, true)
		if err != nil {
			t.Fatal(err)
		}
		tpb, err := tpbForIsolationLevel(level)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.IndexByte(tpb, byte(isc_tpb_nowait)) < 0 || bytes.IndexByte(tpb, byte(isc_tpb_wait)) >= 0 {
			t.Fatalf("opts %+v tpb %v is not NOWAIT", opts, tpb)
		}
	}
}

func TestSnapshotNoWaitConstant(t *testing.T) {
	level, err := txIsolationLevelWith(driver.TxOptions{Isolation: LevelSnapshotNoWait}, false)
	if err != nil {
		t.Fatal(err)
	}
	tpb, err := tpbForIsolationLevel(level)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{byte(isc_tpb_version3), byte(isc_tpb_write), byte(isc_tpb_nowait), byte(isc_tpb_concurrency)}
	if !bytes.Equal(tpb, want) {
		t.Fatalf("tpb = %v, want %v", tpb, want)
	}
}

func TestExplicitTxOnlyRejectsImplicitStatement(t *testing.T) {
	fc := &firebirdsqlConn{isAutocommit: false, explicitTxOnly: true}
	fc.tx = &firebirdsqlTx{fc: fc, needBegin: true}
	if _, err := fc.prepare(context.Background(), "select 1 from rdb$database"); err != ErrImplicitTransaction {
		t.Fatalf("err = %v, want ErrImplicitTransaction", err)
	}
}

func TestExplicitTxOnlyGuardsEveryLazyBeginPath(t *testing.T) {
	cases := map[string]func(*firebirdsqlConn) error{
		"execute immediate": func(fc *firebirdsqlConn) error {
			return fc.ExecImmediate(context.Background(), "select 1 from rdb$database")
		},
		"scrollable query": func(fc *firebirdsqlConn) error {
			_, err := fc.QueryScrollable(context.Background(), "select 1 from rdb$database")
			return err
		},
		"batch prepare": func(fc *firebirdsqlConn) error {
			_, err := fc.PrepareBatch(context.Background(), "insert into t values (1)", BatchOptions{})
			return err
		},
		"prepared statement execute": func(fc *firebirdsqlConn) error {
			stmt := &firebirdsqlStmt{fc: fc}
			_, err := stmt.prepareExecute(context.Background(), nil)
			return err
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			fc := &firebirdsqlConn{wp: &wireProtocol{}, explicitTxOnly: true}
			fc.tx = &firebirdsqlTx{fc: fc, needBegin: true}
			if err := call(fc); err != ErrImplicitTransaction {
				t.Fatalf("err = %v, want ErrImplicitTransaction", err)
			}
		})
	}
}

func TestDefaultsKeepAutocommit(t *testing.T) {
	dsn, err := parseDSN("user:pw@localhost/db.fdb")
	if err != nil {
		t.Fatal(err)
	}
	if dsn.options["explicit_tx_only"] != "false" || dsn.options["tx_nowait"] != "false" {
		t.Fatalf("defaults changed: %v", dsn.options)
	}
}

func TestExplicitTransactionOptionsAcceptOnlyBooleans(t *testing.T) {
	for _, key := range []string{"explicit_tx_only", "tx_nowait"} {
		for _, value := range []string{"true", "false"} {
			if _, err := parseDSN("user:pw@localhost/db.fdb?" + key + "=" + value); err != nil {
				t.Errorf("%s=%q rejected: %v", key, value, err)
			}
		}
		for _, value := range []string{"", "yes", "TRUE", "1"} {
			if _, err := parseDSN("user:pw@localhost/db.fdb?" + key + "=" + value); err == nil {
				t.Errorf("%s=%q accepted", key, value)
			}
		}
	}
}
