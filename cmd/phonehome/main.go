// Command phonehome shows what the devices on your network say about you.
//
//	phonehome serve            run the dashboard and keep ingesting
//	phonehome demo             explore a synthetic household, no setup needed
//	phonehome ingest --once    pull new records from every source and exit
//	phonehome report           print a plain-text report
//	phonehome receipt          write a Privacy Receipt (PNG or SVG)
//	phonehome unknown          list domains the knowledge base can't explain yet
//	phonehome kb lint|stats    check the knowledge base
//	phonehome version
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `phonehome — see what your devices say about you.

Usage:
  phonehome serve   [--config FILE]           dashboard + continuous ingest
  phonehome demo    [--listen ADDR]           try it with a synthetic household
  phonehome ingest  [--config FILE] [--once]  pull records from your sources
  phonehome report  [--config FILE] [--days N]
  phonehome receipt [--config FILE] [--days N] [--device ID] [-o FILE]
  phonehome unknown [--config FILE] [--days N] [--device ID] [--limit N]
                                              unclassified domains, to report
  phonehome kb lint | stats
  phonehome version

Config is read from --config, $PHONEHOME_CONFIG, or ./phonehome.yaml, then
/etc/phonehome/phonehome.yaml. With no config, sources are auto-detected.
Docs: https://github.com/bizpers11991-code/phonehome
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		if !errors.Is(err, errUsage) {
			fmt.Fprintln(os.Stderr, "phonehome:", err)
		}
		os.Exit(1)
	}
}

var errUsage = errors.New("usage")

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "serve":
		return cmdServe(ctx, rest)
	case "demo":
		return cmdDemo(ctx, rest)
	case "ingest":
		return cmdIngest(ctx, rest)
	case "report":
		return cmdReport(ctx, rest)
	case "receipt":
		return cmdReceipt(ctx, rest)
	case "unknown":
		return cmdUnknown(ctx, rest)
	case "kb":
		return cmdKB(rest)
	case "version", "--version", "-v":
		fmt.Println("phonehome", version)
		return nil
	case "help", "--help", "-h":
		fmt.Print(usage)
		return nil
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
	return errUsage
}
