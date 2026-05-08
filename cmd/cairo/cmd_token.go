package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/scotmcc/cairo2/internal/server"
	"github.com/scotmcc/cairo2/internal/store/config"
	"github.com/scotmcc/cairo2/internal/store/sqliteopen"
)

// runToken generates a new server bearer token, saves it to the DB, and prints
// it. Opens the DB only — no LLM server connection required.
func runToken(args []string) error {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: cairo token")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Generate a new server bearer token and save it to the DB.")
		fmt.Fprintln(os.Stderr, "Use with: cairo serve --auth")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	database, err := sqliteopen.Open()
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer database.Close()

	tok, err := server.GenerateToken()
	if err != nil {
		return fmt.Errorf("generate token: %w", err)
	}

	if err := database.Config.Set(config.KeyServerToken, tok); err != nil {
		return fmt.Errorf("save token: %w", err)
	}

	fmt.Println(tok)
	return nil
}
