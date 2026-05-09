package registryserver

import (
	"context"
	"log"
	"time"
)

// StartSweeper runs a background goroutine that marks stale agents every 30 seconds.
func StartSweeper(ctx context.Context, ledger *Ledger) {
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n, err := ledger.Sweep(ctx)
				if err != nil {
					log.Printf("sweeper: %v", err)
					continue
				}
				if n > 0 {
					log.Printf("swept %d stale", n)
				}
			}
		}
	}()
}
