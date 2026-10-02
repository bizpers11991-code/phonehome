// Command devserver serves the dashboard with fixture data for frontend work
// and screenshots:
//
//	go run ./internal/web/internal/devserver -addr :8077 [-demo] [-empty]
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/bizpers11991-code/phonehome/internal/web"
	"github.com/bizpers11991-code/phonehome/internal/web/internal/fixture"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8077", "listen address")
	demo := flag.Bool("demo", false, "label the data as demo data")
	empty := flag.Bool("empty", false, "pretend nothing has been ingested (first-run screen)")
	flag.Parse()

	b := fixture.New(time.Now())
	b.Now = time.Now
	b.Demo, b.Empty = *demo, *empty
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	log.Info("devserver listening", "url", "http://"+*addr)
	if err := http.ListenAndServe(*addr, web.New(b, web.Options{Logger: log})); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}
