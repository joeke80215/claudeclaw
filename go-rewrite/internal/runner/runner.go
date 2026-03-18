// Package runner executes Claude Code prompts serially via a queue.
// This is a stub defining the interface; full implementation is separate.
package runner

// RunResult holds the output from a Claude Code execution.
type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run enqueues a named prompt for serial execution via Claude Code.
func Run(name, prompt string) (*RunResult, error) {
	// TODO: implement
	return &RunResult{}, nil
}

// RunUserMessage prepends a clock prefix and enqueues the prompt.
func RunUserMessage(name, prompt string) (*RunResult, error) {
	// TODO: implement
	return &RunResult{}, nil
}

// Bootstrap creates a new session if none exists.
func Bootstrap() error {
	// TODO: implement
	return nil
}

// EnsureProjectClaudeMd ensures the project CLAUDE.md exists with managed block.
func EnsureProjectClaudeMd() error {
	// TODO: implement
	return nil
}

// LoadHeartbeatPromptTemplate reads the heartbeat prompt template.
func LoadHeartbeatPromptTemplate() (string, error) {
	// TODO: implement
	return "", nil
}
