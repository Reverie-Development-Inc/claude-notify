//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func notifyShutdownSignals(ch chan os.Signal) {
	signal.Notify(ch,
		syscall.SIGINT, syscall.SIGTERM)
}
