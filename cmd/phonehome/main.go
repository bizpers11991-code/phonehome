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
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `phonehome — see what your devices say about you.

Usage:
  phonehome serve   [--config FILE] [--listen ADDR]   dashboard + continuous ingest
  phonehome demo    [--listen ADDR] [--days N] [--seed N] [--metrics]
                                                      try it with a synthetic household
  phonehome ingest  [--config FILE] [--once]          read your sources, no dashboard
  phonehome report  [--config FILE | --demo] [--days N]
                                                      plain-text report of every device
  phonehome receipt [--config FILE | --demo] [--days N] [--device ID] [-o FILE]
                                                      Privacy Receipt image (PNG or SVG)
  phonehome unknown [--config FILE | --demo] [--days N] [--device ID] [--limit N]
                                                      domains the knowledge base can't explain
  phonehome kb lint | stats                           check the knowledge base
  phonehome version

Flags:
  --config FILE  config file; must exist. Default: $PHONEHOME_CONFIG, else
                 ./phonehome.yaml, else /etc/phonehome/phonehome.yaml, else
                 none: sources are auto-detected
  --listen ADDR  address to serve on. Default: $PHONEHOME_LISTEN, else for
                 serve listen: in the config or :8099, for demo 127.0.0.1:8099
  --demo         use the synthetic demo household instead of your data
  --days N       how many days back to cover, ending now (report, receipt,
                 unknown: 7; demo: days of synthetic history, 30)
  --device ID    one device, by ID (mac:aa:bb:cc:dd:ee:ff) or name
  -o FILE        receipt file to write, .png or .svg (default receipt.png)
  --limit N      domain groups to show per device, 0 for all (default 10)
  --once         read what is available now and exit, instead of polling
  --seed N       random seed for the demo household (default 7)
  --metrics      demo only: also serve /metrics for Prometheus

"phonehome COMMAND -h" shows a command's flags.
Docs: https://github.com/bizpers11991-code/phonehome
`

// configHelp describes --config, which most commands take.
const configHelp = "config file; must exist (default: $PHONEHOME_CONFIG, else ./phonehome.yaml, else /etc/phonehome/phonehome.yaml, else auto-detect sources)"

// newFlags is a subcommand's flag set; its -h shows the synopsis, what the
// command does and every flag.
func newFlags(name, synopsis, about string) *flag.FlagSet {
	fl := flag.NewFlagSet(name, flag.ContinueOnError)
	fl.Usage = func() {
		fmt.Fprintf(fl.Output(), "Usage: phonehome %s %s\n\n%s\n\nFlags:\n", name, synopsis, about)
		fl.PrintDefaults()
	}
	return fl
}

// errHelp ends a command that only printed its help: exit status 0.
var errHelp = errors.New("help shown")

// parseFlags parses a subcommand's arguments. -h is errHelp; a bad flag or
// a stray argument (such as a config file named without --config) prints
// the command's usage and is errUsage.
func parseFlags(fl *flag.FlagSet, args []string) error {
	err := fl.Parse(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		return errHelp
	case err != nil:
		return errUsage
	case fl.NArg() > 0:
		fmt.Fprintf(fl.Output(), "unexpected argument %q\n", fl.Arg(0))
		fl.Usage()
		return errUsage
	}
	return nil
}

// init fills in version for builds without -ldflags, such as
// "go install …@v0.3.0", from the module version Go records.
func init() { version = buildVersion(version, debug.ReadBuildInfo) }

// buildVersion is v unless it is "dev" and the build info names a module
// version; "(devel)" means a build from a local checkout.
func buildVersion(v string, read func() (*debug.BuildInfo, bool)) string {
	if v != "dev" {
		return v
	}
	if bi, ok := read(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return v
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, errHelp) {
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
