// budget maintains offline per-tenant/currency limits and billing reconciliation.
// It refuses a running application's DATA_DIR and never contacts a provider.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/adcwb/ai-creative-studio/backend/internal/application"
)

func run() error {
	dir := flag.String("data-dir", "", "existing stopped application's DATA_DIR")
	action := flag.String("action", "report", "report | limit | reserve | unknown | settle | release")
	tenant := flag.String("tenant", "", "organization ID")
	currency := flag.String("currency", "USD", "explicit supported currency; default USD is not an authorized budget")
	amount := flag.Int64("amount-minor", 0, "integer minor units; USD cents, JPY yen, KWD fils")
	id := flag.String("id", "", "stable reservation ID")
	provider := flag.String("provider", "", "provider ID")
	model := flag.String("model", "", "model ID")
	evidence := flag.String("evidence-ref", "", "non-secret billing evidence reference for settlement/release")
	stopped := flag.Bool("confirm-stopped", false, "confirm all API and workers are stopped for PostgreSQL maintenance")
	flag.Parse()
	if *tenant == "" {
		return errors.New("tenant required")
	}
	perform := func(ledger *application.BudgetLedger) error {
		var err error
		switch *action {
		case "report":
		case "limit":
			err = ledger.SetLimit(*tenant, *currency, *amount)
		case "reserve":
			_, err = ledger.Reserve(*id, *tenant, *provider, *model, *currency, *amount)
		case "unknown", "settle", "release":
			state := map[string]string{"unknown": "unknown", "settle": "settled", "release": "released"}[*action]
			err = ledger.Resolve(*id, *tenant, *currency, state, *amount, *evidence)
		default:
			return errors.New("unsupported action")
		}
		if err != nil {
			return err
		}
		entries, err := ledger.Entries(*tenant, *currency)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"currency": *currency, "entries": entries, "operation": "offline_budget_maintenance", "provider_calls_performed": false})
	}
	switch os.Getenv("STORAGE_BACKEND") {
	case "", "file":
		if *dir == "" {
			return errors.New("data-dir and tenant required")
		}
		if info, err := os.Lstat(filepath.Join(*dir, "state.json")); err != nil || !info.Mode().IsRegular() {
			return errors.New("file backend requires existing state.json; refusing PostgreSQL cache or partial data directory")
		}
		info, err := os.Stat(*dir)
		if err != nil || !info.IsDir() {
			return errors.New("existing DATA_DIR required")
		}
		lock, err := os.OpenFile(filepath.Join(*dir, "store.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return err
		}
		defer lock.Close()
		if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return errors.New("stop every server/worker before offline budget administration")
		}
		ledger, err := application.OpenBudgetLedger(*dir)
		if err != nil {
			return err
		}
		return perform(ledger)
	case "postgres":
		if !*stopped || os.Getenv("DATABASE_URL") == "" {
			return errors.New("PostgreSQL maintenance requires DATABASE_URL and --confirm-stopped")
		}
		db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
		if err != nil {
			return errors.New("database configuration invalid")
		}
		defer db.Close()
		db.SetMaxOpenConns(2)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err = application.CheckPostgres(ctx, db); err != nil {
			return err
		}
		return application.WithPostgresBudgetMaintenance(ctx, db, perform, *tenant)
	default:
		return errors.New("STORAGE_BACKEND must be file or postgres")
	}
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "budget operation failed; inspect configuration and billing evidence without automatic retry")
		os.Exit(1)
	}
}
