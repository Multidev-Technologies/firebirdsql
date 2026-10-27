package firebirdsql

import (
	"database/sql/driver"
	"reflect"
	"testing"
)

func TestSQLTypeNames(t *testing.T) {
	cases := []struct {
		x    xSQLVAR
		name string
		scan reflect.Type
	}{
		{xSQLVAR{sqltype: SQL_TYPE_TEXT, sqllen: 10}, "CHAR", reflect.TypeOf("")},
		{xSQLVAR{sqltype: SQL_TYPE_TEXT, sqllen: 8, sqlsubtype: 1}, "CHAR", reflect.TypeOf([]byte(nil))},
		{xSQLVAR{sqltype: SQL_TYPE_VARYING, sqllen: 20}, "VARCHAR", reflect.TypeOf("")},
		{xSQLVAR{sqltype: SQL_TYPE_SHORT}, "SMALLINT", reflect.TypeOf(int64(0))},
		{xSQLVAR{sqltype: SQL_TYPE_LONG}, "INTEGER", reflect.TypeOf(int64(0))},
		{xSQLVAR{sqltype: SQL_TYPE_INT64}, "BIGINT", reflect.TypeOf(int64(0))},
		{xSQLVAR{sqltype: SQL_TYPE_INT64, sqlscale: -2}, "NUMERIC", reflect.TypeOf("")},
		{xSQLVAR{sqltype: SQL_TYPE_LONG, sqlscale: -2}, "NUMERIC", reflect.TypeOf("")},
		{xSQLVAR{sqltype: SQL_TYPE_FLOAT}, "FLOAT", reflect.TypeOf(float64(0))},
		{xSQLVAR{sqltype: SQL_TYPE_DOUBLE}, "DOUBLE PRECISION", reflect.TypeOf(float64(0))},
		{xSQLVAR{sqltype: SQL_TYPE_DATE}, "DATE", nil},
		{xSQLVAR{sqltype: SQL_TYPE_TIME}, "TIME", nil},
		{xSQLVAR{sqltype: SQL_TYPE_TIMESTAMP}, "TIMESTAMP", nil},
		{xSQLVAR{sqltype: SQL_TYPE_BLOB, sqlsubtype: 1}, "BLOB SUB_TYPE TEXT", reflect.TypeOf("")},
		{xSQLVAR{sqltype: SQL_TYPE_BLOB, sqlsubtype: 0}, "BLOB SUB_TYPE 0", reflect.TypeOf([]byte(nil))},
		{xSQLVAR{sqltype: SQL_TYPE_BLOB, sqlsubtype: 7}, "BLOB SUB_TYPE 7", reflect.TypeOf([]byte(nil))},
		{xSQLVAR{sqltype: SQL_TYPE_ARRAY}, "ARRAY", nil},
	}
	for _, c := range cases {
		if got := c.x.sqlTypeName(); got != c.name {
			t.Errorf("%+v name = %q, want %q", c.x, got, c.name)
		}
		if c.scan != nil {
			if got := c.x.sqlScanType(); got != c.scan {
				t.Errorf("%+v scan = %v, want %v", c.x, got, c.scan)
			}
		}
	}
}

func TestNoneTextValuesUnderSQLTypeNames(t *testing.T) {
	char := xSQLVAR{sqltype: SQL_TYPE_TEXT, sqllen: 6}
	v, err := char.valueSQLTypeNames([]byte{'c', 0xE9, ' ', ' ', ' ', ' '}, "", "NONE")
	if err != nil || v != "c\xE9" {
		t.Fatalf("CHAR value = %q, %v", v, err)
	}
	varchar := xSQLVAR{sqltype: SQL_TYPE_VARYING, sqllen: 6}
	v, err = varchar.valueSQLTypeNames([]byte{'c', 0xE9, ' '}, "", "NONE")
	if err != nil || v != "c\xE9 " {
		t.Fatalf("VARCHAR value = %q, %v", v, err)
	}
}

func TestSQLTypeNamesOptionDefaultsFalseAndValidatesBoolean(t *testing.T) {
	dsn, err := parseDSN("user:pw@localhost/db.fdb")
	if err != nil {
		t.Fatal(err)
	}
	if got := dsn.options["sql_type_names"]; got != "false" {
		t.Fatalf("sql_type_names default = %q, want false", got)
	}
	for _, value := range []string{"true", "false"} {
		if _, err := parseDSN("user:pw@localhost/db.fdb?sql_type_names=" + value); err != nil {
			t.Errorf("sql_type_names=%q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"", "yes", "TRUE", "1"} {
		if _, err := parseDSN("user:pw@localhost/db.fdb?sql_type_names=" + value); err == nil {
			t.Errorf("sql_type_names=%q accepted", value)
		}
	}
}

func TestSQLTypeNamesRowsMetadata(t *testing.T) {
	x := []xSQLVAR{
		{sqltype: SQL_TYPE_TEXT, sqllen: 10},
		{sqltype: SQL_TYPE_TEXT, sqllen: 8, sqlsubtype: 1},
		{sqltype: SQL_TYPE_VARYING, sqllen: 20},
		{sqltype: SQL_TYPE_LONG, sqllen: 4, sqlscale: -2},
		{sqltype: SQL_TYPE_BLOB, sqlsubtype: 1},
		{sqltype: SQL_TYPE_BLOB, sqlsubtype: 0},
	}
	rows := &firebirdsqlRows{stmt: &firebirdsqlStmt{
		fc:           &firebirdsqlConn{sqlTypeNames: true, wp: &wireProtocol{charset: "UTF8"}},
		resultXsqlda: x,
	}}
	rows.stmt.fc.wp.charsetLen()
	wantNames := []string{"CHAR", "CHAR", "VARCHAR", "NUMERIC", "BLOB SUB_TYPE TEXT", "BLOB SUB_TYPE 0"}
	wantScans := []reflect.Type{
		reflect.TypeOf(""), reflect.TypeOf([]byte(nil)), reflect.TypeOf(""),
		reflect.TypeOf(""), reflect.TypeOf(""), reflect.TypeOf([]byte(nil)),
	}
	for i := range x {
		if got := rows.ColumnTypeDatabaseTypeName(i); got != wantNames[i] {
			t.Errorf("column %d name = %q, want %q", i, got, wantNames[i])
		}
		if got := rows.ColumnTypeScanType(i); got != wantScans[i] {
			t.Errorf("column %d scan type = %v, want %v", i, got, wantScans[i])
		}
	}
	if got, ok := rows.ColumnTypeLength(0); !ok || got != 2 {
		t.Errorf("UTF8 CHAR length = %d, %v; want 2, true", got, ok)
	}
	if got, ok := rows.ColumnTypeLength(2); !ok || got != 5 {
		t.Errorf("UTF8 VARCHAR length = %d, %v; want 5, true", got, ok)
	}
	if precision, scale, ok := rows.ColumnTypePrecisionScale(3); !ok || precision != 11 || scale != 2 {
		t.Errorf("NUMERIC precision/scale = %d/%d, %v; want 11/2, true", precision, scale, ok)
	}
}

func TestSQLTypeNamesCharacterLengthUsesAttachmentCharset(t *testing.T) {
	for _, tt := range []struct {
		charset string
		want    int64
	}{
		{charset: "NONE", want: 20},
		{charset: "UTF8", want: 5},
	} {
		t.Run(tt.charset, func(t *testing.T) {
			wp := &wireProtocol{charset: tt.charset}
			wp.charsetLen()
			rows := &firebirdsqlRows{stmt: &firebirdsqlStmt{
				fc:           &firebirdsqlConn{sqlTypeNames: true, wp: wp},
				resultXsqlda: []xSQLVAR{{sqltype: SQL_TYPE_TEXT, sqllen: 20}},
			}}
			if got, ok := rows.ColumnTypeLength(0); !ok || got != tt.want {
				t.Fatalf("%s CHAR length = %d, %v; want %d, true", tt.charset, got, ok, tt.want)
			}
		})
	}
}

func TestDefaultTypeMetadataIsUnchanged(t *testing.T) {
	wp := &wireProtocol{charset: "UTF8"}
	wp.charsetLen()
	rows := &firebirdsqlRows{stmt: &firebirdsqlStmt{
		fc:           &firebirdsqlConn{wp: wp},
		resultXsqlda: []xSQLVAR{
			{sqltype: SQL_TYPE_TEXT, sqllen: 10},
			{sqltype: SQL_TYPE_TEXT, sqllen: 8, sqlsubtype: 1},
			{sqltype: SQL_TYPE_LONG, sqlscale: -2},
		},
	}}
	if got := rows.ColumnTypeDatabaseTypeName(0); got != "TEXT" {
		t.Errorf("default database type name = %q, want TEXT", got)
	}
	if got := rows.ColumnTypeScanType(1); got != reflect.TypeOf("") {
		t.Errorf("default OCTETS scan type = %v, want string", got)
	}
	if got, ok := rows.ColumnTypeLength(0); !ok || got != 10 {
		t.Errorf("default UTF8 CHAR length = %d, %v; want 10, true", got, ok)
	}
	if _, scale, ok := rows.ColumnTypePrecisionScale(2); !ok || scale != -2 {
		t.Errorf("default NUMERIC scale = %d, %v; want -2, true", scale, ok)
	}
	defaultValue, err := rows.stmt.resultXsqlda[0].value([]byte("x   "), "", "UTF8")
	if err != nil {
		t.Fatal(err)
	}
	got, err := rows.sqlTypeNamesValue(rows.stmt.resultXsqlda[0], defaultValue)
	if err != nil {
		t.Fatal(err)
	}
	if got != defaultValue || got != "x" {
		t.Errorf("default CHAR value = %q (%T), want unchanged string x", got, got)
	}
}

func TestNoneTextRowsValuesUseRawStringsUnderOption(t *testing.T) {
	wp := &wireProtocol{charset: "NONE"}
	wp.charsetLen()
	rows := &firebirdsqlRows{stmt: &firebirdsqlStmt{fc: &firebirdsqlConn{sqlTypeNames: true, wp: wp}}}
	for _, tt := range []struct {
		name  string
		x     xSQLVAR
		value driver.Value
		want  string
	}{
		{
			name:  "CHAR trims trailing spaces",
			x:     xSQLVAR{sqltype: SQL_TYPE_TEXT},
			value: []byte{'c', 0xE9, ' ', ' '},
			want:  "c\xE9",
		},
		{
			name:  "VARCHAR preserves trailing spaces",
			x:     xSQLVAR{sqltype: SQL_TYPE_VARYING},
			value: []byte{'c', 0xE9, ' '},
			want:  "c\xE9 ",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := rows.sqlTypeNamesValue(tt.x, tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("value = %q (%T), want %q (string)", got, got, tt.want)
			}
		})
	}
}
