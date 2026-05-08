// Package hostedit opens files in whatever editor the user's terminal host
// makes available — VS Code, WaveTerm, Cursor, or JetBrains IDEs — and falls
// back to $EDITOR (or vi) for plain terminals.
//
// Cairo already shells out to bash, ollama, git and friends; rather than
// reinventing a text editor inside a Bubble Tea TUI, we lean on the host
// when one is detected. GUI hosts open files in their own pane/tab without
// disturbing cairo; the terminal fallback needs the caller to suspend its
// TUI program (see WantsTUISuspend).
package hostedit

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Host identifies the detected terminal/editor environment.
type Host int

const (
	HostUnknown   Host = iota // no GUI host detected — terminal fallback
	HostVSCode                // VS Code or Cursor (Cursor sets TERM_PROGRAM=vscode too)
	HostWaveTerm              // WaveTerm — has wsh CLI for split-pane editor
	HostJetBrains             // JetBrains terminal (IntelliJ family)
)

// Detect inspects the environment once and returns the active host.
// Cheap to call — just env var lookups.
func Detect() Host {
	switch strings.ToLower(os.Getenv("TERM_PROGRAM")) {
	case "vscode":
		return HostVSCode
	case "waveterm":
		return HostWaveTerm
	}
	if os.Getenv("WAVETERM_VERSION") != "" || os.Getenv("WAVETERM") != "" {
		return HostWaveTerm
	}
	if os.Getenv("VSCODE_IPC_HOOK_CLI") != "" || os.Getenv("VSCODE_PID") != "" {
		return HostVSCode
	}
	if os.Getenv("TERMINAL_EMULATOR") == "JetBrains-JediTerm" {
		return HostJetBrains
	}
	return HostUnknown
}

// String returns a short label for telemetry/UI ("VS Code", "WaveTerm", ...).
func (h Host) String() string {
	switch h {
	case HostVSCode:
		return "VS Code"
	case HostWaveTerm:
		return "WaveTerm"
	case HostJetBrains:
		return "JetBrains"
	default:
		return "terminal"
	}
}

// WantsTUISuspend reports whether opening an editor will take over the
// terminal. True only for the terminal fallback path (vi/nano/etc); GUI
// hosts open files in a separate pane and cairo can keep rendering.
//
// Callers in a Bubble Tea program should wrap their Open call in
// tea.ExecProcess (or similar) when this returns true.
func WantsTUISuspend() bool {
	return Detect() == HostUnknown
}

// Open launches the best editor for the detected host pointed at path. If
// line > 0 the editor is asked to jump to that line; ignored for hosts that
// don't support it. Returns an error only if the chosen launcher fails to
// start; once an editor is running the call is fire-and-forget.
//
// For HostUnknown the caller is responsible for running this via a
// TUI-suspending wrapper (see WantsTUISuspend) — Open does not perform the
// suspend itself.
func Open(path string, line int) error {
	if path == "" {
		return fmt.Errorf("hostedit: empty path")
	}
	host := Detect()
	cmd, err := buildCmd(host, path, line)
	if err != nil {
		return err
	}
	if host == HostUnknown {
		// Terminal editor: caller must already have suspended its TUI; we
		// inherit stdio so the editor takes over the controlling terminal.
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	// GUI host: detach from cairo's stdio and don't wait. The editor opens
	// in its own pane/tab and cairo keeps rendering.
	return cmd.Start()
}

// buildCmd returns the *exec.Cmd to launch but does not run it. Exposed
// separately so callers (and tests) can inspect or wrap the command.
func buildCmd(host Host, path string, line int) (*exec.Cmd, error) {
	switch host {
	case HostVSCode:
		bin := firstAvailable("code", "code-insiders", "cursor")
		if bin == "" {
			break
		}
		// `-g <path>:<line>` jumps to a specific line; `-r` reuses the
		// current window which is what we want when the user is already in
		// VS Code's terminal.
		target := path
		if line > 0 {
			target = fmt.Sprintf("%s:%d", path, line)
		}
		return exec.Command(bin, "-r", "-g", target), nil

	case HostWaveTerm:
		if bin := firstAvailable("wsh"); bin != "" {
			// `wsh editor <path>` opens in a Wave split pane. wsh does not
			// support a line argument, so we silently drop it.
			return exec.Command(bin, "editor", path), nil
		}

	case HostJetBrains:
		bin := firstAvailable("idea", "pycharm", "goland", "webstorm")
		if bin == "" {
			break
		}
		args := []string{}
		if line > 0 {
			args = append(args, "--line", fmt.Sprintf("%d", line))
		}
		args = append(args, path)
		return exec.Command(bin, args...), nil
	}

	// Fallback — $EDITOR, then vi.
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}
	// Some users set EDITOR to "code --wait" or similar; honor the args.
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		return nil, fmt.Errorf("hostedit: empty $EDITOR")
	}
	args := append(parts[1:], path)
	return exec.Command(parts[0], args...), nil
}

// firstAvailable returns the first command name in candidates whose binary
// is on PATH, or "" if none are.
func firstAvailable(candidates ...string) string {
	for _, c := range candidates {
		if _, err := exec.LookPath(c); err == nil {
			return c
		}
	}
	return ""
}
