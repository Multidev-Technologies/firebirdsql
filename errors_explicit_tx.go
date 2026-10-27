package firebirdsql

import "errors"

// ErrImplicitTransaction is returned, with explicit_tx_only=true, for a
// statement that would otherwise start the connection's autocommit
// transaction, which the driver keeps alive with commit-retaining.
var ErrImplicitTransaction = errors.New("firebirdsql: statement outside an explicit transaction (explicit_tx_only)")
