package firebirdsql

import "testing"

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
