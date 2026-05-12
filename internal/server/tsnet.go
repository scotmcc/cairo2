package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tailscale.com/tsnet"
)

var reNonAlnum = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitizeHostname(raw string) string {
	h := strings.ToLower(raw)
	h = reNonAlnum.ReplaceAllString(h, "-")
	h = strings.Trim(h, "-")
	if len(h) > 63 {
		h = h[:63]
	}
	if h == "" {
		h = "node"
	}
	return h
}

// NewTsnetListener bootstraps a tsnet node and returns a TLS listener on :443.
// cleanup must be called when the caller is done (typically via defer).
// On first run, prints a LoginURL to stdout so the operator can authorize the node.
func NewTsnetListener(ctx context.Context) (net.Listener, *tsnet.Server, func() error, error) {
	raw, _ := os.Hostname()
	bare := raw
	if idx := strings.IndexByte(raw, '.'); idx >= 0 {
		bare = raw[:idx]
	}
	hostname := "cairo-" + sanitizeHostname(bare)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = "~"
	}
	stateDir := filepath.Join(homeDir, ".cairo", "tsnet")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, nil, nil, fmt.Errorf("tsnet state dir: %w", err)
	}

	srv := &tsnet.Server{
		Hostname: hostname,
		Dir:      stateDir,
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
		fmt.Printf("cairo server listening via tailnet\n  url:   https://%s\n", dnsName)
	}

	ln, err := srv.ListenTLS("tcp", ":443")
	if err != nil {
		_ = cleanup()
		return nil, nil, nil, fmt.Errorf("tsnet listen: %w", err)
	}

	return ln, srv, cleanup, nil
}
