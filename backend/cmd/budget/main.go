// budget maintains offline per-tenant/currency limits and billing reconciliation.
// It refuses a running application's DATA_DIR and never contacts a provider.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/adcwb/ai-creative-studio/backend/internal/studio"
	"os"
	"path/filepath"
	"syscall"
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
	flag.Parse()
	if *dir == "" || *tenant == "" {
		return errors.New("data-dir and tenant required")
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
	ledger, err := studio.OpenBudgetLedger(*dir)
	if err != nil {
		return err
	}
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
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"currency": *currency, "entries": entries, "provider_execution_integrated": false})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "budget operation failed:", err)
		os.Exit(1)
	}
}
