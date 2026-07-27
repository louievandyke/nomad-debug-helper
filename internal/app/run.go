package app

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"

	"github.com/louie/nomad-debug-helper/internal/bundle"
	"github.com/louie/nomad-debug-helper/internal/layout"
	"github.com/louie/nomad-debug-helper/internal/model"
	"github.com/louie/nomad-debug-helper/internal/web"
)

const defaultHomepath = "/usr/local/share/grafana"

func Run(args []string) int {
	fs := flag.NewFlagSet("nomad-debug-helper", flag.ContinueOnError)
	listenAddr := fs.String("listen", "127.0.0.1:7676", "HTTP listen address")
	homepath := fs.String("homepath", defaultHomepath, "Homepath for the Grafana installation")
	fs.SetOutput(os.Stderr)

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: nomad-debug-helper [-listen 127.0.0.1:7676] [-homepath ...] <nomad-debug-output-dir-or-archive>")
		return 2
	}

	// Auto-detect known Grafana install locations if the default wasn't
	// overridden, same convenience vault-debug-helper production offers.
	if *homepath == defaultHomepath {
		for _, candidate := range []string{"/usr/share/grafana", "/opt/homebrew/opt/grafana/share/grafana"} {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				*homepath = candidate
				break
			}
		}
	}

	_, portStr, err := net.SplitHostPort(*listenAddr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid -listen address %q: %v\n", *listenAddr, err)
		return 2
	}
	selfPort, err := strconv.Atoi(portStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid -listen port %q: %v\n", portStr, err)
		return 2
	}

	rawBundle, err := bundle.Open(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "open bundle: %v\n", err)
		return 1
	}
	defer func() {
		if err := rawBundle.Cleanup(); err != nil {
			fmt.Fprintf(os.Stderr, "cleanup bundle: %v\n", err)
		}
	}()

	layoutInfo := layout.Detect(rawBundle)
	viewBundle, err := model.Build(rawBundle, layoutInfo)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build model: %v\n", err)
		return 1
	}

	// ctx governs the lifetime of any `go tool pprof`/`go tool trace`
	// subprocesses launched via /analyze, so they're killed on shutdown
	// instead of leaking past this process exiting.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		<-sigCh
		cancel()
	}()

	var wg sync.WaitGroup

	server, err := web.NewServer(ctx, &wg, rawBundle, viewBundle, *homepath, selfPort)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init web server: %v\n", err)
		return 1
	}

	httpServer := &http.Server{Addr: *listenAddr, Handler: server.Routes()}
	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
	}()

	fmt.Printf("Serving %s at http://%s\n", rawBundle.SourcePath, *listenAddr)
	err = httpServer.ListenAndServe()
	cancel()
	wg.Wait()

	if cleanupErr := server.Cleanup(); cleanupErr != nil {
		fmt.Fprintf(os.Stderr, "cleanup grafana work dir: %v\n", cleanupErr)
	}

	if err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "listen: %v\n", err)
		return 1
	}

	return 0
}
