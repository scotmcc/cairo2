package main

import (
	"context"
	"os"
	"syscall"

	"github.com/scotmcc/cairo2/internal/cli"
	"github.com/scotmcc/cairo2/internal/tui"
)

// runOneShot wraps cli.RunOnce.
func runOneShot(app *App, _ context.Context, prompt string) error {
	return cli.RunOnce(app.Agent, prompt)
}

// runCLI wraps cli.Run.
func runCLI(app *App, _ context.Context) error {
	return cli.Run(app.Agent, app.DB, app.Session)
}

// runVSCode wraps cli.RunVSCode.
func runVSCode(app *App, _ context.Context) error {
	return cli.RunVSCode(app.Agent, app.DB, app.Session)
}

// runTUI wraps tui.Run and handles the reload/re-exec pattern. The DB is
// closed before exec so the new process can open it cleanly.
func runTUI(app *App, _ context.Context) error {
	reload, newSession, err := tui.Run(app.Agent, app.DB, app.Session, app.Choices)
	if err != nil {
		return err
	}
	if reload || newSession {
		app.DB.Close()
		exe, eerr := os.Executable()
		if eerr != nil {
			exe = os.Args[0]
		}
		args := os.Args
		if newSession {
			args = append(append([]string{}, os.Args...), "-new")
		}
		if execErr := syscall.Exec(exe, args, os.Environ()); execErr != nil {
			fatalf("reload: %v", execErr)
		}
	}
	return nil
}
