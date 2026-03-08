//go:build windows

package main

import (
	"os"
	"os/signal"
)

func notifyShutdownSignals(ch chan os.Signal) {
	signal.Notify(ch, os.Interrupt)
}
