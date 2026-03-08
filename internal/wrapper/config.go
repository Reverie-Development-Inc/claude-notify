package wrapper

// Config holds paths needed by the PTY wrapper.
type Config struct {
	// ClaudeBinary is the absolute path to the
	// claude CLI.
	ClaudeBinary string
	// RuntimeDir is the XDG_RUNTIME_DIR path for
	// FIFOs.
	RuntimeDir string
	// StateDir is the path for session metadata
	// JSON files.
	StateDir string
}
