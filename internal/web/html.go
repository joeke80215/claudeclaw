package web

// dashboardHTML returns the full HTML page for the ClaudeClaw dashboard.
func dashboardHTML() string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>ClaudeClaw Dashboard</title>
<style>
* { margin: 0; padding: 0; box-sizing: border-box; }
body {
  background: #0a0a0a; color: #e0e0e0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, monospace;
  padding: 20px; max-width: 960px; margin: 0 auto;
}
h1 { color: #bb86fc; margin-bottom: 4px; font-size: 1.6em; }
h2 { color: #03dac6; margin-bottom: 10px; font-size: 1.1em; border-bottom: 1px solid #222; padding-bottom: 6px; }
.subtitle { color: #888; font-size: 0.85em; margin-bottom: 20px; }
.section { background: #141414; border: 1px solid #222; border-radius: 8px; padding: 16px; margin-bottom: 16px; }
.row { display: flex; justify-content: space-between; align-items: center; padding: 6px 0; }
.label { color: #999; font-size: 0.9em; }
.value { color: #e0e0e0; font-weight: 600; font-size: 0.9em; }
.value.ok { color: #4caf50; }
.value.warn { color: #ff9800; }
.value.off { color: #666; }
.toggle-btn {
  background: #222; color: #e0e0e0; border: 1px solid #444; border-radius: 4px;
  padding: 4px 12px; cursor: pointer; font-size: 0.85em;
}
.toggle-btn:hover { background: #333; }
.toggle-btn.active { background: #1b5e20; border-color: #4caf50; color: #4caf50; }
input[type="text"], input[type="number"], textarea {
  background: #1a1a1a; color: #e0e0e0; border: 1px solid #333; border-radius: 4px;
  padding: 6px 10px; font-size: 0.85em; width: 100%;
}
textarea { min-height: 60px; resize: vertical; font-family: monospace; }
.btn {
  background: #bb86fc; color: #0a0a0a; border: none; border-radius: 4px;
  padding: 6px 16px; cursor: pointer; font-weight: 600; font-size: 0.85em;
}
.btn:hover { background: #9c64f0; }
.btn-danger { background: #cf6679; }
.btn-danger:hover { background: #b74c5e; }
.btn-sm { padding: 2px 10px; font-size: 0.8em; }
.job-item {
  display: flex; justify-content: space-between; align-items: center;
  padding: 8px 0; border-bottom: 1px solid #1a1a1a;
}
.job-item:last-child { border-bottom: none; }
.job-name { color: #bb86fc; font-weight: 600; }
.job-schedule { color: #666; font-size: 0.85em; margin-left: 8px; }
.log-area {
  background: #111; border: 1px solid #222; border-radius: 4px; padding: 10px;
  font-family: monospace; font-size: 0.8em; max-height: 300px; overflow-y: auto;
  white-space: pre-wrap; color: #aaa; line-height: 1.5;
}
.modal-overlay {
  display: none; position: fixed; top: 0; left: 0; right: 0; bottom: 0;
  background: rgba(0,0,0,0.7); z-index: 100; justify-content: center; align-items: center;
}
.modal-overlay.show { display: flex; }
.modal {
  background: #1a1a1a; border: 1px solid #333; border-radius: 8px; padding: 24px;
  width: 90%; max-width: 500px;
}
.modal h2 { margin-bottom: 16px; }
.form-group { margin-bottom: 12px; }
.form-group label { display: block; color: #999; font-size: 0.85em; margin-bottom: 4px; }
.form-actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 16px; }
.status-dot { display: inline-block; width: 8px; height: 8px; border-radius: 50%; margin-right: 6px; }
.status-dot.green { background: #4caf50; }
.status-dot.yellow { background: #ff9800; }
.status-dot.red { background: #f44336; }
.create-job-form { display: flex; gap: 8px; align-items: flex-end; flex-wrap: wrap; margin-top: 10px; }
.create-job-form .form-group { margin-bottom: 0; flex: 1; min-width: 120px; }
</style>
</head>
<body>

<h1>ClaudeClaw Dashboard</h1>
<p class="subtitle">Daemon Control Panel</p>

<div class="section" id="status-section">
  <h2>Status</h2>
  <div class="row"><span class="label">Daemon</span><span class="value" id="daemon-status">Loading...</span></div>
  <div class="row"><span class="label">PID</span><span class="value" id="daemon-pid">-</span></div>
  <div class="row"><span class="label">Uptime</span><span class="value" id="daemon-uptime">-</span></div>
  <div class="row"><span class="label">Security</span><span class="value" id="security-level">-</span></div>
</div>

<div class="section" id="heartbeat-section">
  <h2>Heartbeat</h2>
  <div class="row">
    <span class="label">Status</span>
    <span>
      <span class="value" id="heartbeat-status">-</span>
      <button class="toggle-btn" id="heartbeat-toggle" onclick="toggleHeartbeat()">Toggle</button>
    </span>
  </div>
  <div class="row"><span class="label">Interval</span><span class="value" id="heartbeat-interval">-</span></div>
  <div class="row"><span class="label">Next In</span><span class="value" id="heartbeat-countdown">-</span></div>
  <div class="row">
    <span></span>
    <button class="btn btn-sm" onclick="openHeartbeatModal()">Settings</button>
  </div>
</div>

<div class="section" id="jobs-section">
  <h2>Jobs</h2>
  <div id="jobs-list"><span class="label">Loading...</span></div>
  <div class="create-job-form">
    <div class="form-group">
      <label>Name</label>
      <input type="text" id="job-name" placeholder="my-job">
    </div>
    <div class="form-group">
      <label>Schedule (cron or HH:MM)</label>
      <input type="text" id="job-schedule" placeholder="09:00">
    </div>
    <div class="form-group" style="flex:2">
      <label>Prompt</label>
      <input type="text" id="job-prompt" placeholder="Check the weather...">
    </div>
    <button class="btn" onclick="createJob()">Create</button>
  </div>
</div>

<div class="section" id="logs-section">
  <h2>Logs</h2>
  <div class="log-area" id="logs-content">Loading...</div>
  <div style="margin-top:8px;text-align:right">
    <button class="btn btn-sm" onclick="refreshLogs()">Refresh</button>
  </div>
</div>

<!-- Heartbeat Settings Modal -->
<div class="modal-overlay" id="heartbeat-modal">
  <div class="modal">
    <h2>Heartbeat Settings</h2>
    <div class="form-group">
      <label>Interval (minutes)</label>
      <input type="number" id="hb-interval" min="1" max="1440" value="15">
    </div>
    <div class="form-group">
      <label>Prompt</label>
      <textarea id="hb-prompt" placeholder="Custom heartbeat prompt..."></textarea>
    </div>
    <div class="form-actions">
      <button class="btn" style="background:#333;color:#e0e0e0" onclick="closeHeartbeatModal()">Cancel</button>
      <button class="btn" onclick="saveHeartbeatSettings()">Save</button>
    </div>
  </div>
</div>

<script>
let currentState = null;
let heartbeatEnabled = false;

function formatUptime(ms) {
  if (!ms || ms < 0) return '-';
  const totalMin = Math.floor(ms / 60000);
  const h = Math.floor(totalMin / 60);
  const m = totalMin % 60;
  if (h > 0) return h + 'h ' + m + 'm';
  return m + 'm';
}

function formatCountdown(ms) {
  if (!ms || ms <= 0) return 'now';
  const totalSec = Math.floor(ms / 1000);
  const m = Math.floor(totalSec / 60);
  const s = totalSec % 60;
  if (m > 0) return m + 'm ' + s + 's';
  return s + 's';
}

async function fetchState() {
  try {
    const res = await fetch('/api/state');
    const data = await res.json();
    currentState = data;
    updateUI(data);
  } catch (e) {
    document.getElementById('daemon-status').textContent = 'Unreachable';
    document.getElementById('daemon-status').className = 'value warn';
  }
}

function updateUI(data) {
  // Daemon status
  const ds = document.getElementById('daemon-status');
  if (data.daemon && data.daemon.running) {
    ds.innerHTML = '<span class="status-dot green"></span>Running';
    ds.className = 'value ok';
  } else {
    ds.innerHTML = '<span class="status-dot red"></span>Stopped';
    ds.className = 'value warn';
  }
  document.getElementById('daemon-pid').textContent = data.daemon ? data.daemon.pid : '-';
  document.getElementById('daemon-uptime').textContent = data.daemon ? formatUptime(data.daemon.uptimeMs) : '-';
  document.getElementById('security-level').textContent = data.security ? data.security.level : '-';

  // Heartbeat
  if (data.heartbeat) {
    heartbeatEnabled = data.heartbeat.enabled;
    const hs = document.getElementById('heartbeat-status');
    const tb = document.getElementById('heartbeat-toggle');
    if (data.heartbeat.enabled) {
      hs.textContent = 'Enabled';
      hs.className = 'value ok';
      tb.textContent = 'Disable';
      tb.className = 'toggle-btn active';
    } else {
      hs.textContent = 'Disabled';
      hs.className = 'value off';
      tb.textContent = 'Enable';
      tb.className = 'toggle-btn';
    }
    document.getElementById('heartbeat-interval').textContent = data.heartbeat.intervalMinutes + ' min';
    document.getElementById('heartbeat-countdown').textContent =
      data.heartbeat.enabled && data.heartbeat.nextInMs ? formatCountdown(data.heartbeat.nextInMs) : '-';
  }

  // Jobs
  const jobsList = document.getElementById('jobs-list');
  if (data.jobs && data.jobs.length > 0) {
    jobsList.innerHTML = data.jobs.map(function(j) {
      return '<div class="job-item">' +
        '<span><span class="job-name">' + escapeHtml(j.name) + '</span>' +
        '<span class="job-schedule">' + escapeHtml(j.schedule) + '</span></span>' +
        '<button class="btn btn-danger btn-sm" onclick="deleteJob(\'' + escapeHtml(j.name) + '\')">Delete</button>' +
        '</div>';
    }).join('');
  } else {
    jobsList.innerHTML = '<span class="label">No jobs configured</span>';
  }
}

function escapeHtml(str) {
  if (!str) return '';
  return str.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;').replace(/'/g,'&#39;');
}

async function toggleHeartbeat() {
  try {
    await fetch('/api/settings/heartbeat', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({ enabled: !heartbeatEnabled })
    });
    await fetchState();
  } catch (e) { console.error('Toggle heartbeat failed:', e); }
}

async function createJob() {
  const name = document.getElementById('job-name').value.trim();
  const schedule = document.getElementById('job-schedule').value.trim();
  const prompt = document.getElementById('job-prompt').value.trim();
  if (!schedule || !prompt) { alert('Schedule and prompt are required'); return; }
  try {
    const res = await fetch('/api/jobs/quick', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify({ name: name, schedule: schedule, prompt: prompt, recurring: true, notify: 'true' })
    });
    const data = await res.json();
    if (!data.ok) { alert('Error: ' + (data.error || 'Unknown')); return; }
    document.getElementById('job-name').value = '';
    document.getElementById('job-schedule').value = '';
    document.getElementById('job-prompt').value = '';
    await fetchState();
  } catch (e) { alert('Failed to create job: ' + e.message); }
}

async function deleteJob(name) {
  if (!confirm('Delete job "' + name + '"?')) return;
  try {
    const res = await fetch('/api/jobs/' + encodeURIComponent(name), { method: 'DELETE' });
    const data = await res.json();
    if (!data.ok) { alert('Error: ' + (data.error || 'Unknown')); return; }
    await fetchState();
  } catch (e) { alert('Failed to delete job: ' + e.message); }
}

async function refreshLogs() {
  try {
    const res = await fetch('/api/logs?tail=100');
    const data = await res.json();
    const el = document.getElementById('logs-content');
    let lines = [];
    if (data.daemonLog && data.daemonLog.length > 0) {
      lines.push('=== daemon.log ===');
      lines = lines.concat(data.daemonLog);
    }
    if (data.runs && data.runs.length > 0) {
      for (var i = 0; i < data.runs.length; i++) {
        lines.push('\n=== ' + data.runs[i].file + ' ===');
        lines = lines.concat(data.runs[i].lines);
      }
    }
    el.textContent = lines.length > 0 ? lines.join('\n') : 'No logs available';
    el.scrollTop = el.scrollHeight;
  } catch (e) {
    document.getElementById('logs-content').textContent = 'Failed to load logs';
  }
}

function openHeartbeatModal() {
  if (currentState && currentState.heartbeat) {
    document.getElementById('hb-interval').value = currentState.heartbeat.intervalMinutes || 15;
  }
  document.getElementById('heartbeat-modal').classList.add('show');
}

function closeHeartbeatModal() {
  document.getElementById('heartbeat-modal').classList.remove('show');
}

async function saveHeartbeatSettings() {
  const interval = parseInt(document.getElementById('hb-interval').value, 10);
  const prompt = document.getElementById('hb-prompt').value.trim();
  const body = { interval: interval };
  if (prompt) body.prompt = prompt;
  try {
    await fetch('/api/settings/heartbeat', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      body: JSON.stringify(body)
    });
    closeHeartbeatModal();
    await fetchState();
  } catch (e) { alert('Failed to save: ' + e.message); }
}

// Initial load
fetchState();
refreshLogs();

// Auto-refresh every 2 seconds
setInterval(fetchState, 2000);
</script>
</body>
</html>`
}
