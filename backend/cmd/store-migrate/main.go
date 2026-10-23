// store-migrate is an explicit offline operation. It never runs on server
// startup and never guesses a migration direction or overwrites a target.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/studio"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	if err := run(); err != nil {
		// No driver errors/DSNs or paths containing operator secrets on stderr.
		fmt.Fprintln(os.Stderr, "offline migration failed; target may be incomplete; verify before retrying")
		os.Exit(1)
	}
}

func run() error {
	mode := flag.String("mode", "", "init schema, import to empty PostgreSQL or export to a new directory")
	dir := flag.String("directory", "", "complete stopped source directory or new destination")
	stopped := flag.Bool("confirm-stopped", false, "confirm all API and worker processes are stopped")
	flag.Parse()
	if !*stopped || (*mode != "init" && *dir == "") || (*mode != "init" && *mode != "import" && *mode != "export") || flag.NArg() != 0 {
		return errors.New("explicit offline migration arguments required")
	}
	if os.Getenv("DATABASE_URL") == "" {
		return errors.New("DATABASE_URL required")
	}
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		return err
	}
	if err = prepareSchema(ctx, db, *mode); err != nil {
		return err
	}
	if *mode == "init" {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"result": "schema_initialized"})
	}
	if *mode == "import" {
		report, err := studio.ImportPostgresData(ctx, *dir, db)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	report, err := studio.ExportPostgresData(ctx, *dir, db)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report)
}

// Import/export never create or repair a source schema implicitly.
func prepareSchema(ctx context.Context, db *sql.DB, mode string) error {
	if mode == "init" {
		if err := studio.InitPostgres(ctx, db); err != nil {
			return err
		}
	} else if mode != "import" && mode != "export" {
		return errors.New("invalid migration mode")
	}
	return studio.CheckPostgres(ctx, db)
}
