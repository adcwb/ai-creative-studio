package diagnostics

import (
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"os/exec"
	"syscall"
)

// SystemMetadata reports typed numeric/enumerated causes only. Never format the
// error: OS paths and SQL messages/details can contain private data or secrets.
func SystemMetadata(err error) []any {
	var database *pgconn.PgError
	if errors.As(err, &database) {
		code := database.Code
		if len(code) == 5 {
			valid := true
			for _, c := range code {
				if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')) {
					valid = false
				}
			}
			if valid {
				return []any{"error_kind", "database", "sql_state", code}
			}
		}
		return []any{"error_kind", "database"}
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return []any{"error_kind", "system", "os_error", uint64(errno)}
	}
	var process *exec.ExitError
	if errors.As(err, &process) {
		return []any{"error_kind", "process", "exit_status", process.ExitCode()}
	}
	return []any{"error_kind", "unclassified"}
}
