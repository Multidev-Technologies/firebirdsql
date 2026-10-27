package firebirdsql

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSQLDialectOptionDefaultsToThree(t *testing.T) {
	dsn, err := parseDSN("user:pw@localhost/db.fdb")
	if err != nil {
		t.Fatal(err)
	}
	if got := dsn.options["sql_dialect"]; got != "3" {
		t.Fatalf("sql_dialect = %q, want 3", got)
	}
}

func TestSQLDialectOptionAcceptsOneAndThreeOnly(t *testing.T) {
	for _, value := range []string{"1", "3"} {
		if _, err := parseDSN("user:pw@localhost/db.fdb?sql_dialect=" + value); err != nil {
			t.Fatalf("sql_dialect=%s rejected: %v", value, err)
		}
	}
	if _, err := parseDSN("user:pw@localhost/db.fdb?sql_dialect=2"); err == nil {
		t.Fatal("sql_dialect=2 accepted")
	}
}

func TestPrepareStatementPacksConfiguredDialect(t *testing.T) {
	p := &wireProtocol{buf: make([]byte, 0, BUFFER_LEN), sqlDialect: 1}
	p.packPrepareStatement(7, 9, "select 1 from rdb$database")
	if len(p.buf) < 16 {
		t.Fatalf("prepare packet is %d bytes, want at least 16", len(p.buf))
	}
	if got := int32(binary.BigEndian.Uint32(p.buf[12:16])); got != 1 {
		t.Fatalf("packed dialect = %d, want 1", got)
	}
}

func TestAttachPacksConfiguredDialectInDPB(t *testing.T) {
	p, packet := newDialectTestProtocol()
	p.sqlDialect = 1
	p.charset = "UTF8"
	if err := p.opAttach("db.fdb", "user", "pw", ""); err != nil {
		t.Fatal(err)
	}
	assertPacketHasDialectDPB(t, packet.Bytes(), 1)
}

func TestCreatePacksConfiguredDialectInDPB(t *testing.T) {
	p, packet := newDialectTestProtocol()
	p.sqlDialect = 1
	p.charset = "UTF8"
	p.pageSize = 4096
	if err := p.opCreate("db.fdb", "user", "pw", ""); err != nil {
		t.Fatal(err)
	}
	assertPacketHasDialectDPB(t, packet.Bytes(), 1)
}

func TestExecuteImmediatePacksConfiguredDialect(t *testing.T) {
	p, packet := newDialectTestProtocol()
	p.sqlDialect = 1
	if err := p.opExecuteImmediate(9, "select 1 from rdb$database"); err != nil {
		t.Fatal(err)
	}
	if got := int32(binary.BigEndian.Uint32(packet.Bytes()[12:16])); got != 1 {
		t.Fatalf("packed dialect = %d, want 1", got)
	}
}

func TestNewWireProtocolDefaultsToDialectThree(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	p, err := newWireProtocolContext(context.Background(), listener.Addr().String(), "", "UTF8")
	if err != nil {
		t.Fatal(err)
	}
	defer p.conn.Close()
	if got := p.sqlDialect; got != 3 {
		t.Fatalf("wire protocol dialect = %d, want 3", got)
	}
	select {
	case conn := <-accepted:
		_ = conn.Close()
	case <-time.After(time.Second):
		t.Fatal("listener did not accept the wire connection")
	}
}

func TestSQLDialectOneLiveFirebird25(t *testing.T) {
	dsn := os.Getenv("FIREBIRD25_TEST_DSN")
	if dsn == "" {
		t.Skip("FIREBIRD25_TEST_DSN is not set")
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	db, err := sql.Open("firebirdsql", dsn+separator+"sql_dialect=1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	var got string
	if err := tx.QueryRowContext(context.Background(), `SELECT "x" FROM RDB$DATABASE`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "x" {
		t.Fatalf("query result = %q, want x", got)
	}
}

func newDialectTestProtocol() (*wireProtocol, *bytes.Buffer) {
	var packet bytes.Buffer
	p := &wireProtocol{buf: make([]byte, 0, BUFFER_LEN)}
	p.conn.writer = bufio.NewWriter(&packet)
	return p, &packet
}

func assertPacketHasDialectDPB(t *testing.T, packet []byte, dialect byte) {
	t.Helper()
	if len(packet) < 12 {
		t.Fatalf("packet is %d bytes, too short for database name", len(packet))
	}
	databaseNameLength := int(binary.BigEndian.Uint32(packet[8:12]))
	databaseNamePadding := (4 - databaseNameLength%4) % 4
	dpbLengthOffset := 12 + databaseNameLength + databaseNamePadding
	if dpbLengthOffset+4 > len(packet) {
		t.Fatalf("packet is %d bytes, truncated before DPB length", len(packet))
	}
	dpbLength := int(binary.BigEndian.Uint32(packet[dpbLengthOffset : dpbLengthOffset+4]))
	dpbOffset := dpbLengthOffset + 4
	if dpbOffset+dpbLength > len(packet) {
		t.Fatalf("packet is %d bytes, DPB length %d exceeds remaining packet", len(packet), dpbLength)
	}
	dpb := packet[dpbOffset : dpbOffset+dpbLength]
	want := []byte{isc_dpb_sql_dialect, 4, dialect, 0, 0, 0}
	if !bytes.Contains(dpb, want) {
		t.Fatalf("DPB %v does not contain configured SQL dialect %v", dpb, want)
	}
}
