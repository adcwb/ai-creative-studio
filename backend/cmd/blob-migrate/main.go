// blob-migrate performs explicit, stopped-store transfer; it never loads .env,
// shared AWS profiles, instance credentials, or creates buckets/resources.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/adcwb/ai-creative-studio/backend/internal/application"
)

func main() {
	dir := flag.String("data-dir", "", "stopped application DATA_DIR")
	direction := flag.String("direction", "", "to-s3 or to-file")
	flag.Parse()
	if *dir == "" || (*direction != "to-s3" && *direction != "to-file") {
		fmt.Fprintln(os.Stderr, "require -data-dir and -direction=to-s3|to-file")
		os.Exit(2)
	}
	store, e := application.S3BlobStoreFromEnv()
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	report, e := application.MigrateBlobStorage(ctx, *dir, store, *direction == "to-file")
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
	if e = json.NewEncoder(os.Stdout).Encode(report); e != nil {
		os.Exit(1)
	}
}
