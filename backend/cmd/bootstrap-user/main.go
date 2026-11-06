// bootstrap-user is an offline deployment operation, never a registration API.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/application"
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/deployconfig"
	_ "github.com/jackc/pgx/v5/stdlib"
	"io"
	"os"
	"strings"
	"time"
)

func run() error {
	email := flag.String("email", "", "new local account email; password is read from stdin")
	stopped := flag.Bool("confirm-stopped", false, "confirm every API/worker and restart controller is stopped")
	flag.Parse()
	if !*stopped || *email == "" || flag.NArg() != 0 {
		return errors.New("offline confirmation and email required")
	}
	if err := deployconfig.Load(); err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 131))
	if err != nil {
		return err
	}
	if len(raw) > 130 {
		return errors.New("password input too large")
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	password := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if len(password) < 12 || len(password) > 128 {
		return errors.New("invalid password input")
	}
	dir := os.Getenv("DATA_DIR")
	if dir == "" {
		dir = "./data"
	}
	var db *sql.DB
	if os.Getenv("STORAGE_BACKEND") == "postgres" {
		if os.Getenv("DATABASE_URL") == "" {
			return errors.New("database required")
		}
		db, err = sql.Open("pgx", os.Getenv("DATABASE_URL"))
		if err != nil {
			return err
		}
		defer db.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err = application.CheckPostgres(ctx, db); err != nil {
			return err
		}
	} else if v := os.Getenv("STORAGE_BACKEND"); v != "" && v != "file" {
		return errors.New("invalid storage backend")
	}
	user, err := application.BootstrapLocalAccount(dir, db, *email, password)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"user_id": user.ID})
}
func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "offline identity creation failed; no deployment rights were granted; verify state before retrying")
		os.Exit(1)
	}
}
