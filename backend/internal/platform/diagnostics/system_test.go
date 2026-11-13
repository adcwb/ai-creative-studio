package diagnostics

import (
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestSystemMetadataExcludesPathsMessagesAndSQLDetails(t *testing.T) {
	secret := "synthetic-private-secret"
	for _, e := range []error{&os.PathError{Op: secret, Path: secret, Err: syscall.EACCES}, &pgconn.PgError{Code: "42P01", Message: secret, Detail: secret, TableName: secret}, &pgconn.PgError{Code: secret, Message: secret}, errors.New(secret)} {
		raw, _ := json.Marshal(SystemMetadata(e))
		if strings.Contains(string(raw), secret) {
			t.Fatal("raw error metadata leaked")
		}
	}
	raw, _ := json.Marshal(SystemMetadata(&pgconn.PgError{Code: "42P01"}))
	if !strings.Contains(string(raw), "42P01") {
		t.Fatal("SQLSTATE missing")
	}
}
