//go:build !windows

package daemon

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestWriteToFIFO_DeliversContentThenSubmit(t *testing.T) {
	dir := t.TempDir()
	fifoPath := dir + "/test.fifo"
	if err := syscall.Mkfifo(fifoPath, 0600); err != nil {
		t.Fatal(err)
	}

	done := make(chan string, 1)
	go func() {
		f, err := os.Open(fifoPath)
		if err != nil {
			done <- ""
			return
		}
		defer func() { _ = f.Close() }()
		buf := make([]byte, 4096)
		var all []byte
		for {
			n, err := f.Read(buf)
			if n > 0 {
				all = append(all, buf[:n]...)
			}
			if err != nil {
				break
			}
		}
		done <- string(all)
	}()

	time.Sleep(50 * time.Millisecond)

	err := writeToFIFO(fifoPath, "hello world")
	if err != nil {
		t.Fatalf("writeToFIFO: %v", err)
	}

	result := <-done
	if result != "hello world\r" {
		t.Errorf(
			"got %q, want %q",
			result, "hello world\r",
		)
	}
}
