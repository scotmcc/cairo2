package registryserver

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tailscale.com/tsnet"
)

// NewTsnetListener bootstraps a tsnet node named "cairo-registry" and returns a TLS listener on :443.
// stateDir is the parent directory; tsnet state is stored in stateDir/tsnet.
// cleanup must be called when the caller is done (typically via defer).
// Returns the listener, the tsnet.Server (for per-request WhoIs), and cleanup.
func NewTsnetListener(ctx context.Context, stateDir string) (net.Listener, *tsnet.Server, func() error, error) {
	tsnetDir := filepath.Join(stateDir, "tsnet")
	if err := os.MkdirAll(tsnetDir, 0o700); err != nil {
		return nil, nil, nil, fmt.Errorf("tsnet state dir: %w", err)
	}

	srv := &tsnet.Server{
		Hostname: "cairo-registry",
		Dir:      tsnetDir,
		UserLogf: func(string, ...any) {},
	}
	cleanup := func() error { return srv.Close() }

	if err := srv.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("tsnet start: %w", err)
	}

	// Poll for LoginURL in the background so the operator can authorize on first run.
	go func() {
		shown := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
			lc, err := srv.LocalClient()
			if err != nil {
				continue
			}
			st, err := lc.Status(ctx)
			if err != nil {
				continue
			}
			if st.AuthURL != "" && !shown {
				shown = true
				fmt.Printf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\\n", st.AuthURL, st.AuthURL)
				fmt.Printf("  authorize this node: %s\n", st.AuthURL)
			}
			if st.BackendState == "Running" {
				return
			}
		}
	}()

	st, err := srv.Up(ctx)
	if err != nil {
		_ = cleanup()
		return nil, nil, nil, fmt.Errorf("tsnet up: %w", err)
	}

	if st != nil && st.Self != nil {
		dnsName := strings.TrimSuffix(st.Self.DNSName, ".")
		fmt.Printf("cairo-registry listening via tailnet\n  url:   https://%s\n", dnsName)
	}

	ln, err := srv.ListenTLS("tcp", ":443")
	if err != nil {
		_ = cleanup()
		return nil, nil, nil, fmt.Errorf("tsnet listen: %w", err)
	}

	return ln, srv, cleanup, nil
}
