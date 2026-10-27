package firebirdsql

import (
	"errors"
	"testing"
)

func TestDescribeUsesOwnTransactionUnlessExplicitTransactionIsOpen(t *testing.T) {
	for _, tt := range []struct {
		name string
		tx   *firebirdsqlTx
		want bool
	}{
		{name: "no transaction", want: true},
		{name: "open explicit", tx: &firebirdsqlTx{isAutocommit: false}, want: false},
		{name: "retained autocommit", tx: &firebirdsqlTx{isAutocommit: true}, want: true},
		{name: "committed explicit", tx: &firebirdsqlTx{isAutocommit: false, needBegin: true}, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := describeNeedsOwnTransaction(tt.tx); got != tt.want {
				t.Fatalf("describeNeedsOwnTransaction() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFinishDescribeTransactionJoinsErrorsAndKeepsUnresolvedTransaction(t *testing.T) {
	primaryErr := errors.New("prepare failed")
	commitErr := errors.New("commit failed")
	rollbackErr := errors.New("rollback failed")
	fc := &firebirdsqlConn{transactionSet: make(map[*firebirdsqlTx]struct{})}
	previous := &firebirdsqlTx{fc: fc, isAutocommit: true}
	own := &firebirdsqlTx{fc: fc, transHandle: 42}
	fc.tx = own
	fc.transactionSet[own] = struct{}{}
	commitCalls, rollbackCalls := 0, 0

	err := fc.finishDescribeTransaction(own, previous, primaryErr, func() error {
		commitCalls++
		return commitErr
	}, func() error {
		rollbackCalls++
		return rollbackErr
	})

	if !errors.Is(err, primaryErr) || !errors.Is(err, commitErr) || !errors.Is(err, rollbackErr) {
		t.Fatalf("joined error = %v, want prepare, commit and rollback errors", err)
	}
	if commitCalls != 1 || rollbackCalls != 1 {
		t.Fatalf("commit/rollback calls = %d/%d, want 1/1", commitCalls, rollbackCalls)
	}
	if fc.tx != previous {
		t.Fatalf("restored connection tx = %p, want %p", fc.tx, previous)
	}
	if _, ok := fc.transactionSet[own]; !ok {
		t.Fatal("unresolved transaction removed from transactionSet")
	}
}

func TestFinishDescribeTransactionRemovesResolvedTransaction(t *testing.T) {
	for _, tt := range []struct {
		name         string
		commitErr    error
		rollbackErr  error
		wantRollback int
	}{
		{name: "commit succeeds"},
		{name: "rollback succeeds", commitErr: errors.New("commit failed"), wantRollback: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fc := &firebirdsqlConn{transactionSet: make(map[*firebirdsqlTx]struct{})}
			previous := &firebirdsqlTx{fc: fc}
			own := &firebirdsqlTx{fc: fc}
			fc.tx = own
			fc.transactionSet[own] = struct{}{}
			rollbackCalls := 0
			err := fc.finishDescribeTransaction(own, previous, nil, func() error {
				return tt.commitErr
			}, func() error {
				rollbackCalls++
				return tt.rollbackErr
			})
			if tt.commitErr != nil && !errors.Is(err, tt.commitErr) {
				t.Fatalf("error = %v, want commit failure %v", err, tt.commitErr)
			}
			if rollbackCalls != tt.wantRollback {
				t.Fatalf("rollback calls = %d, want %d", rollbackCalls, tt.wantRollback)
			}
			if _, ok := fc.transactionSet[own]; ok {
				t.Fatal("resolved transaction remains in transactionSet")
			}
			if fc.tx != previous {
				t.Fatalf("restored connection tx = %p, want %p", fc.tx, previous)
			}
		})
	}
}

func TestStatementKind(t *testing.T) {
	cases := []struct {
		stmtType int32
		kind     string
		mutating bool
	}{
		{isc_info_sql_stmt_select, "select", false},
		{isc_info_sql_stmt_select_for_upd, "select-for-update", true},
		{isc_info_sql_stmt_insert, "insert", true},
		{isc_info_sql_stmt_update, "update", true},
		{isc_info_sql_stmt_delete, "delete", true},
		{isc_info_sql_stmt_ddl, "ddl", true},
		{isc_info_sql_stmt_exec_procedure, "procedure", true},
		{isc_info_sql_stmt_start_trans, "start-transaction", false},
		{isc_info_sql_stmt_commit, "commit", false},
		{isc_info_sql_stmt_rollback, "rollback", false},
		{isc_info_sql_stmt_set_generator, "set-generator", true},
		{isc_info_sql_stmt_savepoint, "savepoint", false},
		{isc_info_sql_stmt_get_segment, "unsupported", false},
		{99, "unsupported", false},
	}
	for _, c := range cases {
		kind, mutating := statementKind(c.stmtType)
		if kind != c.kind || mutating != c.mutating {
			t.Errorf("type %d = (%s, %v), want (%s, %v)", c.stmtType, kind, mutating, c.kind, c.mutating)
		}
	}
}
