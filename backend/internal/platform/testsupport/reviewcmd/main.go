// Test-only native protocol executable; no model or production credentials.
package main

import (
	"github.com/adcwb/ai-creative-studio/backend/internal/platform/testsupport/reviewfixture"
	"os"
)

func main() {
	if reviewfixture.Command(os.Args[0], os.Stdin, os.Stdout) != nil {
		os.Exit(3)
	}
}
