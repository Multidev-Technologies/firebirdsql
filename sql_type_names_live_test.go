package firebirdsql

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func TestLiveSQLTypeNames(t *testing.T) {
	ctx := context.Background()
	db := firebird25DB(t, "&sql_type_names=true&charset=NONE&explicit_tx_only=true")
	db.SetMaxOpenConns(1)

	setup, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := setup.ExecContext(ctx, `recreate table type_probe (
		c char(4) character set none,
		v varchar(4) character set none,
		o char(2) character set octets,
		n numeric(9,2),
		b blob sub_type text character set none,
		bb blob sub_type 0
	)`); err != nil {
		_ = setup.Rollback()
		t.Fatal(err)
	}
	if err := setup.Commit(); err != nil {
		t.Fatal(err)
	}

	insertTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := insertTx.ExecContext(ctx, `insert into type_probe values (
		'c' || ascii_char(233),
		'c' || ascii_char(233),
		'xy',
		1.23,
		'c' || ascii_char(233),
		x'7879'
	)`); err != nil {
		_ = insertTx.Rollback()
		t.Fatal(err)
	}
	if err := insertTx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "select c, v, o, n, b, bb from type_probe")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"CHAR", "VARCHAR", "CHAR", "NUMERIC", "BLOB SUB_TYPE TEXT", "BLOB SUB_TYPE 0"}
	wantScans := []reflect.Type{
		reflect.TypeOf(""), reflect.TypeOf(""), reflect.TypeOf([]byte(nil)),
		reflect.TypeOf(""), reflect.TypeOf(""), reflect.TypeOf([]byte(nil)),
	}
	if len(columns) != len(wantNames) {
		t.Fatalf("got %d columns, want %d", len(columns), len(wantNames))
	}
	for i, column := range columns {
		if got := column.DatabaseTypeName(); got != wantNames[i] {
			t.Errorf("column %d name = %q, want %q", i, got, wantNames[i])
		}
		if got := column.ScanType(); got != wantScans[i] {
			t.Errorf("column %d scan type = %v, want %v", i, got, wantScans[i])
		}
	}
	if length, ok := columns[0].Length(); !ok || length != 4 {
		t.Errorf("CHAR(4) length = %d, %v; want 4, true", length, ok)
	}
	if _, scale, ok := columns[3].DecimalSize(); !ok || scale != 2 {
		t.Errorf("NUMERIC(9,2) scale = %d, %v; want 2, true", scale, ok)
	}

	if !rows.Next() {
		t.Fatalf("expected one result row, rows.Err() = %v", rows.Err())
	}
	var c, v, n, b string
	var o, bb []byte
	if err := rows.Scan(&c, &v, &o, &n, &b, &bb); err != nil {
		t.Fatal(err)
	}
	if c != "c\xE9" {
		t.Errorf("CHAR value = %q, want raw c\\xE9", c)
	}
	if v != "c\xE9" {
		t.Errorf("VARCHAR value = %q, want raw c\\xE9", v)
	}
	if string(o) != "xy" {
		t.Errorf("OCTETS value = %v, want xy", o)
	}
	if b != "c\xE9" {
		t.Errorf("text BLOB value = %q, want raw c\\xE9", b)
	}
	if !reflect.DeepEqual(bb, []byte("xy")) {
		t.Errorf("binary BLOB value = %v, want [120 121]", bb)
	}
	if rows.Next() {
		t.Fatal("expected exactly one result row")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
