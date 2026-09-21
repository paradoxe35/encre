// Command gen-catalog rebuilds the shipped model list from Hugging Face:
//
//	go run ./cmd/gen-catalog
//
// It writes internal/stt/models.json, the offline and first-run fallback, so
// a release ships with whatever the hub published at the time.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/paradoxe35/encre/internal/stt"
)

func main() {
	out := flag.String("o", "internal/stt/models.json", "where to write the catalogue")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	fmt.Fprintln(os.Stderr, "building the catalogue from Hugging Face...")
	catalog, err := stt.FetchCatalog(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	for _, model := range catalog.Models {
		fmt.Fprintf(os.Stderr, "  %-38s %8.1f MB  %-7s %3d lang\n",
			model.Slug, model.SizeMB(), model.Quant, len(model.Languages))
	}

	data, err := stt.EncodeCatalog(catalog)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "\nwrote %d models to %s\n", len(catalog.Models), *out)
}
