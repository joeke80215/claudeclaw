// Package web provides the HTTP dashboard server.
// This is a stub defining the interface; full implementation is separate.
package web

import (
	"github.com/claudeclaw/claudeclaw/internal/config"
	"github.com/claudeclaw/claudeclaw/internal/jobs"
)

// Snapshot holds the current daemon state for the web UI.
type Snapshot struct {
	PID             int
	StartedAt       int64
	HeartbeatNextAt int64
	Settings        *config.Settings
	Jobs            []jobs.Job
}

// ServerHandle represents a running web server that can be stopped.
type ServerHandle struct {
	Host string
	Port int
	stop func()
}

// Stop shuts down the web server.
func (h *ServerHandle) Stop() {
	if h.stop != nil {
		h.stop()
	}
}

// HeartbeatPatch holds partial heartbeat settings from the web UI.
type HeartbeatPatch struct {
	Enabled        *bool
	Interval       *int
	Prompt         *string
	ExcludeWindows *[]config.HeartbeatExcludeWindow
}

// Options configures the web server.
type Options struct {
	Host                       string
	Port                       int
	GetSnapshot                func() Snapshot
	OnHeartbeatEnabledChanged  func(enabled bool)
	OnHeartbeatSettingsChanged func(patch HeartbeatPatch)
	OnJobsChanged              func()
}

// StartWebUI starts the HTTP server. Returns a handle to stop it.
func StartWebUI(opts Options) (*ServerHandle, error) {
	// TODO: implement
	return &ServerHandle{Host: opts.Host, Port: opts.Port}, nil
}
