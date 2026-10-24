// Package obtop is the public SDK for OceanBase activity monitoring.
//
// Import path: github.com/chen8103/obtop
//
// Contract summary:
//
//   - Open creates a Client that owns the underlying database connection.
//     Call Close to release it. Concurrent Snapshot/Watch/QuerySQLHistory/
//     ExplainSQL calls on one Client are safe; each Watch uses an independent
//     rate baseline.
//   - Context cancellation and deadlines are honored. Methods return ctx.Err()
//     when cancelled.
//   - The SDK never starts a TUI, never calls os.Exit, and never writes to
//     stdout/stderr by itself.
//   - Management actions (KillSession, DrainUserSessions) are explicit methods
//     and are never triggered by Snapshot/Watch/history/explain.
//   - Install the public module with:
//     go get github.com/chen8103/obtop@latest
package obtop
