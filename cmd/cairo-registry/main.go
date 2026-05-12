package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"tailscale.com/tsnet"

	"github.com/scotmcc/cairo2/internal/audit"
	"github.com/scotmcc/cairo2/internal/registryserver"
)

var version = "dev"

func main() {
	stateDir := defaultStateDir()
	var addr string
	var adminAddr string
	var noTsnet bool
	var versionFlag bool
	var bootstrapSuperAdmin string
	flag.StringVar(&stateDir, "state-dir", stateDir, "state directory (default ~/.cairo-registry)")
	flag.StringVar(&addr, "addr", ":443", "listen address (--no-tsnet only)")
	flag.StringVar(&adminAddr, "admin-addr", "127.0.0.1:8081", "loopback admin listener address; empty string disables")
	flag.BoolVar(&noTsnet, "no-tsnet", false, "listen on plain TCP for local dev")
	flag.BoolVar(&versionFlag, "version", false, "print version and exit")
	flag.StringVar(&bootstrapSuperAdmin, "bootstrap-super-admin", "", "seed this user as super-admin on first boot (idempotent)")
	flag.Parse()

	if versionFlag {
		fmt.Println(version)
		os.Exit(0)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		log.Fatalf("state dir: %v", err)
	}

	ledger, err := registryserver.OpenLedger(filepath.Join(stateDir, "registry.db"))
	if err != nil {
		log.Fatalf("ledger: %v", err)
	}
	defer ledger.Close()

	sink := audit.NewSQLiteSink(ledger.DB())
	audit.SetDefaultSink(sink)
	log.Printf("audit sink: sqlite (%s)", filepath.Join(stateDir, "registry.db"))

	if bootstrapSuperAdmin != "" {
		if err := ledger.AddSuperAdmin(ctx, bootstrapSuperAdmin); err != nil {
			log.Fatalf("bootstrap super-admin: %v", err)
		}
		log.Printf("bootstrap super-admin: %s", bootstrapSuperAdmin)
	}

	startedAt := time.Now()

	var ln net.Listener
	var tsnetSrv *tsnet.Server
	var cleanup func() error

	if noTsnet {
		ln, err = net.Listen("tcp", addr)
		if err != nil {
			log.Fatalf("listen: %v", err)
		}
		log.Printf("cairo-registry listening on %s (no-tsnet)", addr)
	} else {
		ln, tsnetSrv, cleanup, err = registryserver.NewTsnetListener(ctx, stateDir)
		if err != nil {
			log.Fatalf("tsnet: %v", err)
		}
		defer cleanup()
	}

	if adminAddr != "" {
		adminMux := registryserver.NewAdmin(ledger, startedAt, sink)
		adminSrv := &http.Server{Handler: registryserver.LogRequests(adminMux)}
		adminLn, err := net.Listen("tcp", adminAddr)
		if err != nil {
			log.Fatalf("admin listen: %v", err)
		}
		log.Printf("cairo-registry admin listening on %s", adminAddr)
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = adminSrv.Shutdown(shutdownCtx)
		}()
		go func() {
			if err := adminSrv.Serve(adminLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("admin serve: %v", err)
			}
		}()
	}

	registryserver.StartSweeper(ctx, ledger)

	srv := registryserver.New(ledger, tsnetSrv, startedAt)
	if err := srv.Serve(ctx, ln); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cairo-registry"
	}
	return filepath.Join(home, ".cairo-registry")
}
