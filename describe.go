package firebirdsql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// ColumnOrigin is the base relation and field an output column reads; both
// are empty for expressions.
type ColumnOrigin struct{ Relation, Field string }

// StatementDescriptor classifies a prepared statement without running it.
type StatementDescriptor struct {
	// Kind is one of "select", "select-for-update", "insert", "update",
	// "delete", "ddl", "procedure", "start-transaction", "commit",
	// "rollback", "set-generator", "savepoint" or "unsupported".
	Kind        string
	ReturnsRows bool
	// Mutating is false for select, unsupported, transaction-control and
	// savepoint kinds and true for every other recognized kind.
	Mutating   bool
	InputCount int
	Columns    []ColumnOrigin
}

// DescribeStatement prepares query on conn, classifies it and frees it
// without executing. It uses the connection's open explicit transaction when
// there is one; otherwise it starts a read-only read-committed transaction
// and commits it before returning.
func DescribeStatement(ctx context.Context, conn *sql.Conn, query string) (StatementDescriptor, error) {
	var out StatementDescriptor
	err := conn.Raw(func(driverConn any) error {
		fc, ok := driverConn.(*firebirdsqlConn)
		if !ok {
			return fmt.Errorf("firebirdsql: DescribeStatement needs a firebirdsql connection, got %T", driverConn)
		}
		var err error
		out, err = fc.describeStatement(ctx, query)
		return err
	})
	return out, err
}

func (fc *firebirdsqlConn) describeStatement(ctx context.Context, query string) (StatementDescriptor, error) {
	var out StatementDescriptor
	err := fc.wp.withContextDeadline(ctx, func() (err error) {
		if fc.tx == nil || fc.tx.needBegin {
			previous := fc.tx
			own, beginErr := newFirebirdsqlTx(fc, ISOLATION_LEVEL_READ_COMMITED_RO, false, true)
			if beginErr != nil {
				return beginErr
			}
			fc.tx = own
			defer func() {
				commitErr := own.fc.wp.opCommit(own.transHandle)
				if commitErr == nil {
					_, _, _, commitErr = own.fc.wp.opResponse()
				}
				delete(fc.transactionSet, own)
				fc.tx = previous
				if err == nil {
					err = commitErr
				}
			}()
		}

		stmt, prepErr := newFirebirdsqlStmt(fc, query)
		if prepErr != nil {
			return prepErr
		}
		defer func() {
			if closeErr := stmt.Close(); err == nil {
				err = closeErr
			}
		}()

		inputs, bindErr := fc.wp._fetchBindXsqlda(stmt.stmtHandle)
		if bindErr != nil {
			return bindErr
		}
		out.Kind, out.Mutating = statementKind(stmt.stmtType)
		out.ReturnsRows = len(stmt.resultXsqlda) > 0
		out.InputCount = len(inputs)
		out.Columns = make([]ColumnOrigin, len(stmt.resultXsqlda))
		for i, x := range stmt.resultXsqlda {
			relation := strings.TrimRight(x.relname, " ")
			field := strings.TrimRight(x.fieldname, " ")
			if relation == "" {
				// Firebird can report a synthetic field name (for example
				// "ADD") for an expression. It is not a base-column origin.
				field = ""
			}
			out.Columns[i] = ColumnOrigin{Relation: relation, Field: field}
		}
		return nil
	})
	return out, err
}

func statementKind(stmtType int32) (string, bool) {
	switch stmtType {
	case isc_info_sql_stmt_select:
		return "select", false
	case isc_info_sql_stmt_select_for_upd:
		return "select-for-update", true
	case isc_info_sql_stmt_insert:
		return "insert", true
	case isc_info_sql_stmt_update:
		return "update", true
	case isc_info_sql_stmt_delete:
		return "delete", true
	case isc_info_sql_stmt_ddl:
		return "ddl", true
	case isc_info_sql_stmt_exec_procedure:
		return "procedure", true
	case isc_info_sql_stmt_start_trans:
		return "start-transaction", false
	case isc_info_sql_stmt_commit:
		return "commit", false
	case isc_info_sql_stmt_rollback:
		return "rollback", false
	case isc_info_sql_stmt_set_generator:
		return "set-generator", true
	case isc_info_sql_stmt_savepoint:
		return "savepoint", false
	default:
		return "unsupported", false
	}
}
