package firebirdsql

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
)

func TestNoneTextFallback(t *testing.T) {
	cases := []struct {
		raw  []byte
		want string
	}{
		{[]byte("plain"), "plain"},
		{[]byte("caf\xc3\xa9"), "café"},
		{[]byte("caf\xe9"), "café"},
		{[]byte("\x80uro"), "€uro"},
	}
	for _, c := range cases {
		got, err := applyNoneTextFallback(c.raw, "WIN1252")
		if err != nil || got != c.want {
			t.Errorf("%q = %q, %v; want %q", c.raw, got, err, c.want)
		}
	}
	for _, c := range []struct {
		charset string
		raw     []byte
		want    string
	}{
		{charset: "WIN1250", raw: []byte{0xA3}, want: "Ł"},
		{charset: "ISO8859_1", raw: []byte{0xE9}, want: "é"},
	} {
		got, err := applyNoneTextFallback(c.raw, c.charset)
		if err != nil || got != c.want {
			t.Errorf("%s %q = %q, %v; want %q", c.charset, c.raw, got, err, c.want)
		}
	}
	if _, err := applyNoneTextFallback([]byte{0x81}, "WIN1252"); err == nil {
		t.Error("undefined WIN1252 byte 0x81 accepted")
	}
	if _, err := parseDSN("u:p@h/db?none_text_fallback=KOI8R"); err == nil {
		t.Error("unsupported fallback accepted")
	}
}

func TestNoneTextFallbackDSNOption(t *testing.T) {
	dsn, err := parseDSN("u:p@h/db")
	if err != nil {
		t.Fatal(err)
	}
	if got := dsn.options["none_text_fallback"]; got != "" {
		t.Fatalf("default none_text_fallback = %q, want empty", got)
	}
	for _, value := range []string{"WIN1252", "WIN1250", "ISO8859_1", "win1252"} {
		if _, err := parseDSN("u:p@h/db?none_text_fallback=" + value); err != nil {
			t.Errorf("none_text_fallback=%q rejected: %v", value, err)
		}
	}
}

func TestNoneTextFallbackRowValues(t *testing.T) {
	for _, tt := range []struct {
		name         string
		sqlTypeNames bool
		x            xSQLVAR
		value        driver.Value
		want         driver.Value
	}{
		{
			name:  "CHAR trims spaces without sql_type_names",
			x:     xSQLVAR{sqltype: SQL_TYPE_TEXT},
			value: []byte{'c', 'a', 'f', 0xE9, ' ', ' '},
			want:  "café",
		},
		{
			name:         "CHAR trims spaces with sql_type_names",
			sqlTypeNames: true,
			x:            xSQLVAR{sqltype: SQL_TYPE_TEXT},
			value:        []byte{'c', 'a', 'f', 0xE9, ' ', ' '},
			want:         "café",
		},
		{
			name:  "VARCHAR preserves spaces without sql_type_names",
			x:     xSQLVAR{sqltype: SQL_TYPE_VARYING},
			value: []byte{'c', 'a', 'f', 0xE9, ' '},
			want:  "café ",
		},
		{
			name:         "VARCHAR preserves spaces with sql_type_names",
			sqlTypeNames: true,
			x:            xSQLVAR{sqltype: SQL_TYPE_VARYING},
			value:        []byte{'c', 'a', 'f', 0xE9, ' '},
			want:         "café ",
		},
		{
			name:  "valid UTF-8 remains unchanged",
			x:     xSQLVAR{sqltype: SQL_TYPE_VARYING},
			value: []byte("café"),
			want:  "café",
		},
		{
			name:  "OCTETS remains bytes",
			x:     xSQLVAR{sqltype: SQL_TYPE_VARYING, sqlsubtype: 1},
			value: []byte{'c', 0xE9},
			want:  []byte{'c', 0xE9},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rows := &firebirdsqlRows{stmt: &firebirdsqlStmt{fc: &firebirdsqlConn{
				wp:               &wireProtocol{charset: "NONE"},
				sqlTypeNames:     tt.sqlTypeNames,
				noneTextFallback: "WIN1252",
			}}}
			got, err := rows.sqlTypeNamesValue(tt.x, tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("value = %#v (%T), want %#v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

func TestNoneTextFallbackDecodeErrorKeepsWireSynchronized(t *testing.T) {
	var frame acceptFrame
	xsqlda := []xSQLVAR{
		{sqltype: SQL_TYPE_VARYING},
		{sqltype: SQL_TYPE_LONG},
	}
	frame.int32(op_fetch_response)
	frame.int32(0)
	frame.int32(1)
	frame.buf.Write([]byte{2, 0, 0, 0}) // first column present, second column NULL
	frame.int32(1)
	frame.buf.WriteByte(0x81)
	frame.buf.Write(make([]byte, 3))
	frame.int32(op_fetch_response)
	frame.int32(100)
	frame.int32(0)
	frame.opResponseFrame(42, nil)
	wp := testProtocol(frame.bytes())
	wp.charset = "NONE"
	wp.protocolVersion = PROTOCOL_VERSION13
	chunk, moreData, err := wp.opFetchResponse(1, 1, xsqlda)
	if err != nil {
		t.Fatalf("fetch raw row: %v", err)
	}
	if moreData {
		t.Fatal("fetch reported more data after final batch")
	}
	rows := &firebirdsqlRows{
		ctx: context.Background(),
		stmt: &firebirdsqlStmt{
			fc:           &firebirdsqlConn{wp: wp, noneTextFallback: "WIN1252"},
			resultXsqlda: xsqlda,
		},
		currentChunk:    chunk,
		currentChunkIdx: -1,
	}
	if err := rows.Next(make([]driver.Value, 2)); err == nil || !strings.Contains(err.Error(), "undefined in WIN1252") {
		t.Fatalf("Next error = %v, want undefined WIN1252 byte error", err)
	}
	if rows.badConn || wp.desynced {
		t.Fatalf("decode error poisoned synchronized connection: rows.badConn=%v wire.desynced=%v", rows.badConn, wp.desynced)
	}
	got, _, _, err := wp.opResponse()
	if err != nil {
		t.Fatalf("read response after text decode error: %v", err)
	}
	if got != 42 {
		t.Fatalf("next response handle = %d, want 42", got)
	}
}

func TestNoneTextFallbackBlobValues(t *testing.T) {
	for _, tt := range []struct {
		name         string
		procedure    bool
		sqlTypeNames bool
		subtype      int
		blob         []byte
		want         driver.Value
	}{
		{name: "select text BLOB without sql_type_names", subtype: 1, blob: []byte{'c', 'a', 'f', 0xE9}, want: "café"},
		{name: "select text BLOB with sql_type_names", sqlTypeNames: true, subtype: 1, blob: []byte{'c', 'a', 'f', 0xE9}, want: "café"},
		{name: "procedure text BLOB without sql_type_names", procedure: true, subtype: 1, blob: []byte{'c', 'a', 'f', 0xE9}, want: "café"},
		{name: "procedure text BLOB with sql_type_names", procedure: true, sqlTypeNames: true, subtype: 1, blob: []byte{'c', 'a', 'f', 0xE9}, want: "café"},
		{name: "procedure OCTETS BLOB remains bytes", procedure: true, sqlTypeNames: true, subtype: 0, blob: []byte{'c', 0xE9}, want: []byte{'c', 0xE9}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var frame acceptFrame
			frame.opResponseFrame(1, nil) // open BLOB
			segments := make([]byte, 0, len(tt.blob)+4)
			half := len(tt.blob) / 2
			for _, part := range [][]byte{tt.blob[:half], tt.blob[half:]} {
				segments = append(segments, byte(len(part)), byte(len(part)>>8))
				segments = append(segments, part...)
			}
			frame.opResponseFrame(2, segments) // final segment response
			frame.opResponseFrame(0, nil)      // close BLOB
			frame.opResponseFrame(43, nil)     // wire synchronization sentinel

			wp := testProtocol(frame.bytes())
			wp.charset = "NONE"
			stmt := &firebirdsqlStmt{
				stmtType:     isc_info_sql_stmt_select,
				fc:           &firebirdsqlConn{wp: wp, tx: &firebirdsqlTx{transHandle: 1}, sqlTypeNames: tt.sqlTypeNames, noneTextFallback: "WIN1252"},
				resultXsqlda: []xSQLVAR{{sqltype: SQL_TYPE_BLOB, sqlsubtype: tt.subtype}},
			}
			id := make([]byte, 8)
			binary.LittleEndian.PutUint64(id, 1)
			rows := &firebirdsqlRows{ctx: context.Background(), stmt: stmt, currentChunk: [][]driver.Value{{id}}, currentChunkIdx: -1}
			if tt.procedure {
				stmt.stmtType = isc_info_sql_stmt_exec_procedure
				rows.currentChunk = nil
				rows.result = []driver.Value{id}
			}
			dest := make([]driver.Value, 1)
			if err := rows.Next(dest); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(dest[0], tt.want) {
				t.Fatalf("BLOB value = %#v (%T), want %#v (%T)", dest[0], dest[0], tt.want, tt.want)
			}
			if got, _, _, err := wp.opResponse(); err != nil || got != 43 {
				t.Fatalf("response after BLOB materialization = %d, %v; want handle 43", got, err)
			}
		})
	}
}

func TestNoneTextFallbackBlobDecodeErrorKeepsWireSynchronized(t *testing.T) {
	var frame acceptFrame
	frame.opResponseFrame(1, nil)
	frame.opResponseFrame(2, []byte{1, 0, 0x81})
	frame.opResponseFrame(0, nil)
	frame.opResponseFrame(44, nil)
	wp := testProtocol(frame.bytes())
	wp.charset = "NONE"
	stmt := &firebirdsqlStmt{
		stmtType:     isc_info_sql_stmt_select,
		fc:           &firebirdsqlConn{wp: wp, tx: &firebirdsqlTx{transHandle: 1}, noneTextFallback: "WIN1252"},
		resultXsqlda: []xSQLVAR{{sqltype: SQL_TYPE_BLOB, sqlsubtype: 1}},
	}
	rows := &firebirdsqlRows{ctx: context.Background(), stmt: stmt, currentChunk: [][]driver.Value{{make([]byte, 8)}}, currentChunkIdx: -1}
	err := rows.Next(make([]driver.Value, 1))
	if err == nil || !strings.Contains(err.Error(), "undefined in WIN1252") {
		t.Fatalf("Next error = %v, want undefined WIN1252 byte error", err)
	}
	if rows.badConn || wp.desynced {
		t.Fatalf("decode error poisoned synchronized connection: rows.badConn=%v wire.desynced=%v", rows.badConn, wp.desynced)
	}
	got, _, _, err := wp.opResponse()
	if err != nil || got != 44 {
		t.Fatalf("response after BLOB decode error = %d, %v; want handle 44", got, err)
	}
}
