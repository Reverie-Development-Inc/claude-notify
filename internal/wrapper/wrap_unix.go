//go:build !windows

// Package wrapper provides a PTY relay that wraps
// Claude Code, merging real terminal stdin with a FIFO
// for reply injection. Discord replies written to the
// FIFO appear as keyboard input to the wrapped process.
package wrapper

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/creack/pty/v2"
	"golang.org/x/term"

	"github.com/Reverie-Development-Inc/claude-notify/internal/session"
)

// Run starts Claude Code inside a PTY, creates a FIFO for
// stdin injection, and blocks until the child process exits.
// It returns nil on clean exit or propagates the exit code.
func Run(cfg Config, args []string) error {
	pid := os.Getpid()

	// Ensure directories exist with secure permissions.
	for _, dir := range []string{cfg.RuntimeDir, cfg.StateDir} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// Create FIFO for reply injection.
	fifoPath := filepath.Join(
		cfg.RuntimeDir,
		fmt.Sprintf("%d.fifo", pid),
	)
	if err := syscall.Mkfifo(fifoPath, 0600); err != nil {
		if !errors.Is(err, syscall.EEXIST) {
			return fmt.Errorf("mkfifo: %w", err)
		}
		// Stale FIFO from crashed wrapper — remove
		// and recreate.
		_ = os.Remove(fifoPath)
		if err := syscall.Mkfifo(
			fifoPath, 0600,
		); err != nil {
			return fmt.Errorf("mkfifo retry: %w", err)
		}
	}
	defer func() { _ = os.Remove(fifoPath) }()

	// Write initial session metadata so the daemon can
	// discover this session.
	cwd, _ := os.Getwd()
	shortID := fmt.Sprintf("%04x", pid&0xFFFF)
	metaPath := filepath.Join(
		cfg.StateDir,
		fmt.Sprintf("%d.json", pid),
	)
	meta := &session.Metadata{
		PID:     pid,
		FIFO:    fifoPath,
		CWD:     cwd,
		Started: time.Now(),
		Status:  session.StatusActive,
		ShortID: shortID,
	}
	if err := session.Write(metaPath, meta); err != nil {
		return fmt.Errorf("write metadata: %w", err)
	}
	defer func() { _ = os.Remove(metaPath) }()

	// Build command with env vars so hooks can locate the
	// session metadata and FIFO.
	cmd := exec.Command(cfg.ClaudeBinary, args...) // #nosec G204 -- binary path from user config
	cmd.Env = append(
		os.Environ(),
		"CLAUDE_NOTIFY_SESSION="+metaPath,
		"CLAUDE_NOTIFY_FIFO="+fifoPath,
	)

	// Start the child inside a PTY.
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("pty start: %w", err)
	}
	defer func() { _ = ptmx.Close() }()

	// Forward SIGWINCH to keep the PTY size in sync with
	// the real terminal.
	winchCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	go func() {
		for range winchCh {
			_ = pty.InheritSize(os.Stdin, ptmx)
		}
	}()
	// Trigger an initial size sync.
	winchCh <- syscall.SIGWINCH

	// Forward SIGINT and SIGTERM to the child process so
	// Ctrl-C and kill signals propagate correctly.
	termCh := make(chan os.Signal, 1)
	signal.Notify(termCh,
		syscall.SIGINT, syscall.SIGTERM)
	go func() {
		for sig := range termCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	// Put the real terminal into raw mode so keystrokes
	// pass through unmodified.
	if term.IsTerminal(int(os.Stdin.Fd())) {
		oldState, err := term.MakeRaw(
			int(os.Stdin.Fd()),
		)
		if err != nil {
			return fmt.Errorf("raw mode: %w", err)
		}
		defer func() {
			_ = term.Restore(
				int(os.Stdin.Fd()), oldState,
			)
		}()
	}

	// Forward: real stdin -> PTY master.
	go func() {
		_, _ = io.Copy(ptmx, os.Stdin)
	}()

	// Forward: FIFO -> PTY master.
	// The goroutine reopens the FIFO after each writer
	// closes, blocking until the next writer connects.
	go func() {
		for {
			f, err := os.Open(fifoPath) // #nosec G304 -- path constructed by this process
			if err != nil {
				return // FIFO removed means exit
			}
			_, _ = io.Copy(ptmx, f)
			_ = f.Close()
		}
	}()

	// Forward: PTY master -> real stdout.
	// This blocks until the child exits (EOF on PTY).
	_, _ = io.Copy(os.Stdout, ptmx)

	// Wait for the child to exit and propagate its code.
	if err := cmd.Wait(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		return err
	}
	return nil
}
