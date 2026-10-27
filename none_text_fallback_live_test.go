package firebirdsql

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
)

func TestLiveNoneTextFallback(t *testing.T) {
	ctx := context.Background()
	baseDSN := os.Getenv("FIREBIRD25_TEST_DSN")
	if baseDSN == "" {
		t.Skip("FIREBIRD25_TEST_DSN is not set")
	}
	const fixtureDatabase = "/firebird/data/fixture.fdb"
	if !strings.Contains(baseDSN, fixtureDatabase) {
		t.Fatalf("fixture DSN %q does not contain expected database path %q", baseDSN, fixtureDatabase)
	}
	// The fixture is disposable. Create a database whose default character set
	// is NONE, matching the attachments that need legacy catalog decoding.
	databaseDSN := strings.Replace(baseDSN, fixtureDatabase, "/firebird/data/none-text-fallback.fdb", 1)
	creator, err := sql.Open("firebirdsql_createdb", databaseDSN)
	if err != nil {
		t.Fatal(err)
	}
	creator.SetMaxOpenConns(1)
	if _, err := creator.ExecContext(ctx, "select 1 from rdb$database"); err != nil {
		_ = creator.Close()
		t.Fatalf("create NONE database: %v", err)
	}
	if err := creator.Close(); err != nil {
		t.Fatal(err)
	}

	// Compile through a WIN1252 attachment into the NONE database. The driver
	// encodes the Go UTF-8 literal to CP1252 on the wire; metadata readers later
	// attach with NONE and need the fallback option below.
	setupDSN := strings.Replace(databaseDSN, "charset=NONE", "charset=WIN1252", 1)
	setupDB, err := sql.Open("firebirdsql", setupDSN+"&explicit_tx_only=true")
	if err != nil {
		t.Fatal(err)
	}
	setupDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = setupDB.Close() })
	setup, err := setupDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	procedure := "create or alter procedure none_text_fallback_probe returns (v varchar(16) character set none) as begin v = 'café'; suspend; end"
	if _, err := setup.ExecContext(ctx, procedure); err != nil {
		_ = setup.Rollback()
		t.Fatal(err)
	}
	if _, err := setup.ExecContext(ctx, "create table none_text_fallback_values (v varchar(16) character set none, b blob sub_type text character set none)"); err != nil {
		_ = setup.Rollback()
		t.Fatal(err)
	}
	if err := setup.Commit(); err != nil {
		t.Fatal(err)
	}
	setup, err = setupDB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Firebird 2.5 normalizes DDL source literals to UTF-8 in
	// RDB$PROCEDURE_SOURCE. Seed a VARCHAR and text BLOB with server-generated
	// CP1252 bytes so the fixture also exercises the fallback decode itself.
	if _, err := setup.ExecContext(ctx, `insert into none_text_fallback_values values (
		cast('caf' || ascii_char(233) as varchar(16) character set none),
		cast('caf' || ascii_char(233) as blob sub_type text character set none)
	)`); err != nil {
		_ = setup.Rollback()
		t.Fatal(err)
	}
	if err := setup.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := setupDB.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("firebirdsql", databaseDSN+"&none_text_fallback=WIN1252&sql_type_names=true&explicit_tx_only=true")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	readDecoded := func(db *sql.DB) (string, string, string, error) {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return "", "", "", err
		}
		defer tx.Rollback()
		var varcharValue, textBlobValue, procedureSource string
		if err := tx.QueryRowContext(ctx, "select v, b from none_text_fallback_values").Scan(&varcharValue, &textBlobValue); err != nil {
			return "", "", "", err
		}
		if err := tx.QueryRowContext(ctx, "select rdb$procedure_source from rdb$procedures where rdb$procedure_name = 'NONE_TEXT_FALLBACK_PROBE'").Scan(&procedureSource); err != nil {
			return "", "", "", err
		}
		if err := tx.Commit(); err != nil {
			return "", "", "", err
		}
		return varcharValue, textBlobValue, procedureSource, nil
	}
	gotVarchar, gotTextBlob, gotSource, err := readDecoded(db)
	if err != nil {
		t.Fatal(err)
	}
	if gotVarchar != "café" {
		t.Errorf("fallback VARCHAR = %q, want café", gotVarchar)
	}
	if gotTextBlob != "café" {
		t.Errorf("fallback text BLOB = %q, want café", gotTextBlob)
	}
	if !strings.Contains(gotSource, "'café'") {
		t.Errorf("fallback procedure source %q does not contain CP1252 literal decoded as café", gotSource)
	}

	rawDB, err := sql.Open("firebirdsql", databaseDSN+"&sql_type_names=true&explicit_tx_only=true")
	if err != nil {
		t.Fatal(err)
	}
	rawDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = rawDB.Close() })
	rawTx, err := rawDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer rawTx.Rollback()
	var rawVarchar, rawTextBlob, rawSource []byte
	if err := rawTx.QueryRowContext(ctx, "select v, b from none_text_fallback_values").Scan(&rawVarchar, &rawTextBlob); err != nil {
		t.Fatal(err)
	}
	if err := rawTx.QueryRowContext(ctx, "select rdb$procedure_source from rdb$procedures where rdb$procedure_name = 'NONE_TEXT_FALLBACK_PROBE'").Scan(&rawSource); err != nil {
		t.Fatal(err)
	}
	if err := rawTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawVarchar, []byte("caf\xe9")) {
		t.Errorf("raw VARCHAR = %q (%v), want caf\\xe9 bytes", rawVarchar, rawVarchar)
	}
	if !bytes.Equal(rawTextBlob, []byte("caf\xe9")) {
		t.Errorf("raw text BLOB = %q (%v), want caf\\xe9 bytes", rawTextBlob, rawTextBlob)
	}
	if !bytes.Contains(rawSource, []byte("'café'")) {
		t.Errorf("raw procedure source = %q (%v), want UTF-8 source retained by Firebird 2.5", rawSource, rawSource)
	}
}
