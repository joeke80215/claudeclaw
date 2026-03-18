package web

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

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

// HeartbeatPatch represents a partial update to heartbeat settings.
type HeartbeatPatch struct {
	Enabled        *bool                          `json:"enabled,omitempty"`
	Interval       *int                           `json:"interval,omitempty"`
	Prompt         *string                        `json:"prompt,omitempty"`
	ExcludeWindows []config.HeartbeatExcludeWindow `json:"excludeWindows,omitempty"`
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

// Handle represents a running web server.
type Handle struct {
	Host string
	Port int
	srv  *http.Server
}

// StartWebUI starts the web dashboard HTTP server.
func StartWebUI(opts Options) (*Handle, error) {
	mux := http.NewServeMux()

	// GET / or /index.html — serve dashboard HTML
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(dashboardHTML()))
	})

	// GET /api/health
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{"ok": true, "now": time.Now().UnixMilli()})
	})

	// GET /api/state
	mux.HandleFunc("/api/state", func(w http.ResponseWriter, r *http.Request) {
		snapshot := opts.GetSnapshot()
		writeJSON(w, BuildState(snapshot))
	})

	// GET /api/settings
	mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/settings" {
			// Let /api/settings/heartbeat be handled by its own handler
			http.NotFound(w, r)
			return
		}
		snapshot := opts.GetSnapshot()
		if snapshot.Settings != nil {
			writeJSON(w, SanitizeSettings(*snapshot.Settings))
		} else {
			writeJSON(w, SanitizeSettings(config.Settings{}))
		}
	})

	// GET/POST /api/settings/heartbeat
	mux.HandleFunc("/api/settings/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			snapshot := opts.GetSnapshot()
			if snapshot.Settings != nil {
				writeJSON(w, map[string]interface{}{
					"ok":        true,
					"heartbeat": snapshot.Settings.Heartbeat,
				})
			} else {
				writeJSON(w, map[string]interface{}{
					"ok":        true,
					"heartbeat": config.HeartbeatConfig{},
				})
			}
			return
		}

		if r.Method == http.MethodPost {
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeJSON(w, map[string]interface{}{"ok": false, "error": "invalid JSON"})
				return
			}

			patch := HeartbeatPatch{}
			hasField := false

			if v, ok := body["enabled"]; ok {
				b := toBool(v)
				patch.Enabled = &b
				hasField = true
			}
			if v, ok := body["interval"]; ok {
				n, err := toFloat64(v)
				if err != nil || !isFinite(n) {
					writeJSON(w, map[string]interface{}{"ok": false, "error": "interval must be numeric"})
					return
				}
				iv := clampInt(int(math.Round(n)), 1, 1440)
				patch.Interval = &iv
				hasField = true
			}
			if v, ok := body["prompt"]; ok {
				s := fmt.Sprintf("%v", v)
				patch.Prompt = &s
				hasField = true
			}
			if v, ok := body["excludeWindows"]; ok {
				arr, ok := v.([]interface{})
				if !ok {
					writeJSON(w, map[string]interface{}{"ok": false, "error": "excludeWindows must be an array"})
					return
				}
				windows := parseExcludeWindowsFromAPI(arr)
				patch.ExcludeWindows = windows
				hasField = true
			}

			if !hasField {
				writeJSON(w, map[string]interface{}{"ok": false, "error": "no heartbeat fields provided"})
				return
			}

			if opts.OnHeartbeatEnabledChanged != nil && patch.Enabled != nil {
				opts.OnHeartbeatEnabledChanged(*patch.Enabled)
			}
			if opts.OnHeartbeatSettingsChanged != nil {
				opts.OnHeartbeatSettingsChanged(patch)
			}

			writeJSON(w, map[string]interface{}{"ok": true})
			return
		}

		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})

	// GET /api/technical-info
	mux.HandleFunc("/api/technical-info", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, BuildTechnicalInfo())
	})

	// POST /api/jobs/quick
	mux.HandleFunc("/api/jobs/quick", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var body struct {
			Name      string `json:"name"`
			Schedule  string `json:"schedule"`
			Time      string `json:"time"`
			Prompt    string `json:"prompt"`
			Recurring *bool  `json:"recurring"`
			Daily     *bool  `json:"daily"`
			Notify    string `json:"notify"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": "invalid JSON"})
			return
		}

		schedule := body.Schedule
		if schedule == "" {
			schedule = body.Time
		}

		recurring := true
		if body.Recurring != nil {
			recurring = *body.Recurring
		} else if body.Daily != nil {
			recurring = *body.Daily
		}

		if err := CreateQuickJob(body.Name, schedule, body.Prompt, recurring, body.Notify); err != nil {
			writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}

		if opts.OnJobsChanged != nil {
			opts.OnJobsChanged()
		}
		writeJSON(w, map[string]interface{}{"ok": true})
	})

	// GET /api/jobs and DELETE /api/jobs/{name}
	mux.HandleFunc("/api/jobs/", func(w http.ResponseWriter, r *http.Request) {
		// Handle DELETE /api/jobs/{name}
		if r.Method == http.MethodDelete {
			encodedName := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
			name, err := url.PathUnescape(encodedName)
			if err != nil {
				writeJSON(w, map[string]interface{}{"ok": false, "error": "invalid job name encoding"})
				return
			}
			if err := DeleteJob(name); err != nil {
				writeJSON(w, map[string]interface{}{"ok": false, "error": err.Error()})
				return
			}
			if opts.OnJobsChanged != nil {
				opts.OnJobsChanged()
			}
			writeJSON(w, map[string]interface{}{"ok": true})
			return
		}

		http.NotFound(w, r)
	})

	mux.HandleFunc("/api/jobs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		snapshot := opts.GetSnapshot()
		jobList := make([]map[string]interface{}, 0, len(snapshot.Jobs))
		for _, j := range snapshot.Jobs {
			preview := j.Prompt
			if len(preview) > 160 {
				preview = preview[:160]
			}
			jobList = append(jobList, map[string]interface{}{
				"name":          j.Name,
				"schedule":      j.Schedule,
				"promptPreview": preview,
			})
		}
		writeJSON(w, map[string]interface{}{"jobs": jobList})
	})

	// GET /api/logs
	mux.HandleFunc("/api/logs", func(w http.ResponseWriter, r *http.Request) {
		tail := 100
		if v := r.URL.Query().Get("tail"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				tail = clampInt(n, 20, 2000)
			}
		}
		writeJSON(w, ReadLogs(tail))
	})

	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("web: listen %s: %w", addr, err)
	}

	// Get the actual port (in case opts.Port was 0)
	actualPort := listener.Addr().(*net.TCPAddr).Port

	srv := &http.Server{
		Handler: mux,
	}

	handle := &Handle{
		Host: opts.Host,
		Port: actualPort,
		srv:  srv,
	}

	go srv.Serve(listener)

	return handle, nil
}

// Stop gracefully shuts down the web server.
func (h *Handle) Stop() {
	if h.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.srv.Shutdown(ctx)
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func clampInt(n, min, max int) int {
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func toBool(v interface{}) bool {
	switch b := v.(type) {
	case bool:
		return b
	case float64:
		return b != 0
	case string:
		return b == "true" || b == "1"
	default:
		return false
	}
}

func toFloat64(v interface{}) (float64, error) {
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	case string:
		return strconv.ParseFloat(n, 64)
	default:
		return 0, fmt.Errorf("not numeric")
	}
}

func isFinite(f float64) bool {
	return !math.IsInf(f, 0) && !math.IsNaN(f)
}

func parseExcludeWindowsFromAPI(arr []interface{}) []config.HeartbeatExcludeWindow {
	var windows []config.HeartbeatExcludeWindow
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		start, _ := m["start"].(string)
		end, _ := m["end"].(string)
		start = strings.TrimSpace(start)
		end = strings.TrimSpace(end)

		var days []int
		if rawDays, ok := m["days"].([]interface{}); ok {
			for _, d := range rawDays {
				if n, ok := d.(float64); ok {
					iv := int(n)
					if iv >= 0 && iv <= 6 {
						days = append(days, iv)
					}
				}
			}
		}

		windows = append(windows, config.HeartbeatExcludeWindow{
			Start: start,
			End:   end,
			Days:  days,
		})
	}
	return windows
}
