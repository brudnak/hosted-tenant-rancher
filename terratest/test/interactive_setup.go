package test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/spf13/viper"
)

type interactivePhase string

const (
	phaseEditor    interactivePhase = "editor"
	phaseResolving interactivePhase = "resolving"
	phaseReview    interactivePhase = "review"
	phaseDone      interactivePhase = "done"
)

type interactiveEvent struct {
	Type  string           `json:"type"`
	Phase interactivePhase `json:"phase,omitempty"`
	Line  string           `json:"line,omitempty"`
	Plan  string           `json:"plan,omitempty"`
	Error string           `json:"error,omitempty"`
}

type interactiveResult struct {
	plans []*RancherResolvedPlan
	err   error
}

type interactiveServer struct {
	token      string
	configPath string

	mu          sync.Mutex
	phase       interactivePhase
	logs        []string
	planText    string
	resolveErr  string
	plans       []*RancherResolvedPlan
	subscribers []chan interactiveEvent
	submitted   bool

	resultCh chan interactiveResult
}

func resolveRancherSetup() ([]*RancherResolvedPlan, error) {
	mode := strings.ToLower(strings.TrimSpace(viper.GetString("rancher.mode")))
	autoApprove := viper.GetBool("rancher.auto_approve")

	if mode == "auto" && !autoApprove {
		return runInteractiveAutoModeSetup()
	}

	plans, err := prepareRancherConfiguration(getTotalRancherInstances())
	if err != nil {
		return nil, err
	}
	return plans, nil
}

func runInteractiveAutoModeSetup() ([]*RancherResolvedPlan, error) {
	configPath := strings.TrimSpace(viper.ConfigFileUsed())
	if configPath == "" {
		return nil, fmt.Errorf("failed to determine tool-config.yml path for interactive setup")
	}

	versions := currentPreflightVersions()
	for len(versions) < 2 {
		versions = append(versions, "")
	}

	token, err := randomConfirmationToken()
	if err != nil {
		return nil, fmt.Errorf("failed to create interactive setup token: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to start interactive setup listener: %w", err)
	}

	srv := &interactiveServer{
		token:      token,
		configPath: configPath,
		phase:      phaseEditor,
		resultCh:   make(chan interactiveResult, 1),
	}

	mux := http.NewServeMux()
	srv.registerHandlers(mux, versions)

	server := &http.Server{Handler: mux}
	serverErrCh := make(chan error, 1)
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErrCh <- serveErr
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	setupURL := fmt.Sprintf("http://%s/?token=%s", listener.Addr().String(), token)
	if err := openBrowser(setupURL); err != nil {
		return nil, fmt.Errorf("failed to open interactive setup page: %w", err)
	}
	log.Printf("[setup] Opened interactive setup at %s", setupURL)

	select {
	case result := <-srv.resultCh:
		srv.broadcast(interactiveEvent{Type: "phase", Phase: phaseDone})
		return result.plans, result.err
	case serveErr := <-serverErrCh:
		return nil, fmt.Errorf("interactive setup server failed: %w", serveErr)
	case <-time.After(45 * time.Minute):
		return nil, fmt.Errorf("timed out waiting for interactive setup response")
	}
}

func (s *interactiveServer) registerHandlers(mux *http.ServeMux, initialVersions []string) {
	initialVersionsJSON, _ := json.Marshal(initialVersions)

	pageTemplate := template.Must(template.New("interactive-setup").Parse(interactiveSetupHTML))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, "invalid interactive setup token", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pageTemplate.Execute(w, struct {
			Token               string
			ConfigPath          string
			InitialVersionsJSON template.JS
		}{
			Token:               s.token,
			ConfigPath:          s.configPath,
			InitialVersionsJSON: template.JS(string(initialVersionsJSON)),
		})
	})

	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, "invalid interactive setup token", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req struct {
			Versions []string `json:"versions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		normalizedVersions, err := normalizePreflightVersions(req.Versions)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := updateAutoModeConfigFile(s.configPath, normalizedVersions); err != nil {
			http.Error(w, fmt.Sprintf("failed to update tool-config.yml: %v", err), http.StatusInternalServerError)
			return
		}

		s.mu.Lock()
		if s.submitted {
			s.mu.Unlock()
			writeJSON(w, map[string]string{"status": "already_running"})
			return
		}
		s.submitted = true
		s.phase = phaseResolving
		s.logs = nil
		s.mu.Unlock()

		s.broadcast(interactiveEvent{Type: "phase", Phase: phaseResolving})
		writeJSON(w, map[string]string{"status": "resolving"})

		go s.runResolution()
	})

	mux.HandleFunc("/respond", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, "invalid interactive setup token", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "failed to parse form", http.StatusBadRequest)
			return
		}

		action := r.FormValue("action")
		s.mu.Lock()
		plans := s.plans
		s.phase = phaseDone
		s.mu.Unlock()

		s.broadcast(interactiveEvent{Type: "phase", Phase: phaseDone})

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Setup</title><script>setTimeout(function(){window.close();},300);</script><style>body{font-family:ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;padding:32px;background:#f6f1e8;color:#1d1a16}</style></head><body><p>Your response was recorded. You can close this tab.</p></body></html>`)

		select {
		case s.resultCh <- func() interactiveResult {
			if action == "continue" {
				return interactiveResult{plans: plans, err: nil}
			}
			return interactiveResult{plans: nil, err: fmt.Errorf("user canceled interactive Rancher setup")}
		}():
		default:
		}
	})

	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, "invalid interactive setup token", http.StatusForbidden)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		s.mu.Lock()
		phase := s.phase
		logsCopy := append([]string(nil), s.logs...)
		planText := s.planText
		resolveErr := s.resolveErr
		sub := make(chan interactiveEvent, 256)
		s.subscribers = append(s.subscribers, sub)
		s.mu.Unlock()
		defer s.removeSubscriber(sub)

		writeSSE(w, flusher, interactiveEvent{Type: "phase", Phase: phase})
		for _, line := range logsCopy {
			writeSSE(w, flusher, interactiveEvent{Type: "log", Line: line})
		}
		if planText != "" {
			writeSSE(w, flusher, interactiveEvent{Type: "plan", Plan: planText})
		}
		if resolveErr != "" {
			writeSSE(w, flusher, interactiveEvent{Type: "error", Error: resolveErr})
		}

		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-sub:
				if !ok {
					return
				}
				writeSSE(w, flusher, ev)
				if ev.Type == "phase" && ev.Phase == phaseDone {
					return
				}
			case <-heartbeat.C:
				fmt.Fprint(w, ": heartbeat\n\n")
				flusher.Flush()
			}
		}
	})
}

func (s *interactiveServer) authorized(r *http.Request) bool {
	if strings.TrimSpace(r.URL.Query().Get("token")) == s.token {
		return true
	}
	if r.FormValue("token") == s.token {
		return true
	}
	return requestFromLoopback(r) && sameOriginBrowserRequest(r)
}

func (s *interactiveServer) runResolution() {
	tap := &logTap{}
	tap.onLine = func(line string) { s.appendLog(line) }

	originalWriter := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(io.MultiWriter(originalWriter, tap))
	defer func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
	}()

	plans, err := prepareRancherConfiguration(getTotalRancherInstances())
	if err == nil {
		logResolvedPlans(plans)
	}

	tap.flush()

	if err != nil {
		s.mu.Lock()
		s.resolveErr = err.Error()
		s.mu.Unlock()
		s.broadcast(interactiveEvent{Type: "error", Error: err.Error()})
		select {
		case s.resultCh <- interactiveResult{plans: nil, err: fmt.Errorf("plan resolution failed: %w", err)}:
		default:
		}
		return
	}

	planText := buildResolvedPlansDialogMessage(plans)

	s.mu.Lock()
	s.plans = plans
	s.planText = planText
	s.phase = phaseReview
	s.mu.Unlock()

	s.broadcast(interactiveEvent{Type: "plan", Plan: planText})
	s.broadcast(interactiveEvent{Type: "phase", Phase: phaseReview})
}

func (s *interactiveServer) appendLog(line string) {
	s.mu.Lock()
	s.logs = append(s.logs, line)
	subs := append([]chan interactiveEvent(nil), s.subscribers...)
	s.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub <- interactiveEvent{Type: "log", Line: line}:
		default:
		}
	}
}

func (s *interactiveServer) broadcast(ev interactiveEvent) {
	s.mu.Lock()
	subs := append([]chan interactiveEvent(nil), s.subscribers...)
	s.mu.Unlock()

	for _, sub := range subs {
		select {
		case sub <- ev:
		default:
		}
	}
}

func (s *interactiveServer) removeSubscriber(target chan interactiveEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sub := range s.subscribers {
		if sub == target {
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			return
		}
	}
}

func writeSSE(w io.Writer, flusher http.Flusher, ev interactiveEvent) {
	payload, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", payload)
	flusher.Flush()
}

type logTap struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	onLine func(string)
}

func (t *logTap) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n, err := t.buf.Write(p)
	for {
		data := t.buf.Bytes()
		idx := bytes.IndexByte(data, '\n')
		if idx < 0 {
			break
		}
		line := string(data[:idx])
		t.buf.Next(idx + 1)
		if strings.TrimSpace(line) != "" && t.onLine != nil {
			t.onLine(line)
		}
	}
	return n, err
}

func (t *logTap) flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.buf.Len() == 0 {
		return
	}
	line := strings.TrimRight(t.buf.String(), "\r\n")
	t.buf.Reset()
	if strings.TrimSpace(line) != "" && t.onLine != nil {
		t.onLine(line)
	}
}

const interactiveSetupHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Hosted/Tenant Rancher Setup</title>
  <style>
    :root {
      color-scheme: light dark;
      --bg: #f4efe7;
      --panel: rgba(255, 252, 247, 0.96);
      --text: #1d1a16;
      --muted: #665d52;
      --border: rgba(78, 62, 43, 0.18);
      --accent: #1e6a52;
      --accent-strong: #16513f;
      --secondary: rgba(127, 108, 91, 0.14);
      --danger: #9b3b2a;
      --shadow: 0 24px 80px rgba(55, 39, 20, 0.18);
      --log-bg: #1d1a16;
      --log-fg: #f3efe8;
    }
    @media (prefers-color-scheme: dark) {
      :root {
        --bg: #1a1d1c;
        --panel: rgba(34, 38, 37, 0.96);
        --text: #f3efe8;
        --muted: #b7ada2;
        --border: rgba(209, 196, 178, 0.16);
        --accent: #5bc29c;
        --accent-strong: #3ea882;
        --secondary: rgba(127, 108, 91, 0.18);
        --danger: #ff9d8f;
        --shadow: 0 24px 80px rgba(0, 0, 0, 0.4);
        --log-bg: #0f1210;
        --log-fg: #e7e0d2;
      }
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      font-family: ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background:
        radial-gradient(circle at top left, rgba(32, 120, 92, 0.14), transparent 34%),
        radial-gradient(circle at top right, rgba(175, 118, 52, 0.14), transparent 28%),
        linear-gradient(180deg, rgba(255,255,255,0.18), transparent 48%),
        var(--bg);
      color: var(--text);
      display: grid;
      place-items: center;
      padding: 24px;
    }
    .shell {
      width: min(980px, 100%);
      background: var(--panel);
      border: 1px solid var(--border);
      border-radius: 22px;
      box-shadow: var(--shadow);
      overflow: hidden;
      backdrop-filter: blur(14px);
    }
    .header {
      padding: 24px 28px 12px;
      display: flex;
      align-items: center;
      gap: 14px;
    }
    h1 {
      margin: 0;
      font-size: clamp(1.45rem, 2.2vw, 2rem);
      line-height: 1.15;
    }
    .subtitle {
      margin: 10px 0 0;
      color: var(--muted);
      font-size: 0.98rem;
      max-width: 70ch;
    }
    .body { padding: 0 20px 20px; }
    section[data-section] { display: none; }
    body[data-phase="editor"] section[data-section="editor"] { display: block; }
    body[data-phase="resolving"] section[data-section="resolving"] { display: block; }
    body[data-phase="review"] section[data-section="review"] { display: block; }
    body[data-phase="done"] section[data-section="done"] { display: block; }

    .panel {
      border: 1px solid var(--border);
      border-radius: 18px;
      background: rgba(0, 0, 0, 0.03);
      padding: 16px;
    }
    .row-header, .row {
      display: grid;
      grid-template-columns: 130px minmax(0, 1fr) 100px;
      gap: 12px;
      align-items: center;
    }
    .row-header {
      color: var(--muted);
      font-size: 0.8rem;
      text-transform: uppercase;
      letter-spacing: 0.04em;
      padding: 0 6px 8px;
    }
    .rows { display: grid; gap: 10px; }
    .row {
      padding: 10px 6px;
      border-top: 1px solid var(--border);
    }
    .row:first-child { border-top: 0; }
    .instance-label { font-weight: 800; }
    .instance-role {
      font-size: 0.75rem;
      color: var(--muted);
      font-weight: 400;
    }
    input[type="text"] {
      width: 100%;
      border: 1px solid var(--border);
      background: transparent;
      color: var(--text);
      border-radius: 12px;
      padding: 11px 13px;
      font: inherit;
    }
    .summary {
      margin-top: 14px;
      display: grid;
      gap: 8px;
      color: var(--muted);
      font-size: 0.94rem;
    }
    .summary strong { color: var(--text); }
    .error {
      margin-top: 14px;
      min-height: 1.4em;
      color: var(--danger);
      font-weight: 600;
      white-space: pre-wrap;
    }
    .status {
      margin-top: 10px;
      color: var(--muted);
      font-size: 0.94rem;
      min-height: 1.4em;
    }
    .actions {
      display: flex;
      justify-content: space-between;
      gap: 12px;
      padding: 18px 20px 22px;
      border-top: 1px solid var(--border);
    }
    .left-actions, .right-actions {
      display: flex;
      gap: 12px;
      flex-wrap: wrap;
    }
    button {
      appearance: none;
      border: 0;
      border-radius: 999px;
      padding: 11px 18px;
      font: inherit;
      font-weight: 700;
      cursor: pointer;
      transition: transform 120ms ease, opacity 120ms ease, background 120ms ease;
    }
    button:hover { transform: translateY(-1px); }
    button:active { transform: translateY(0); }
    button:disabled { opacity: 0.6; cursor: default; transform: none; }
    .secondary { background: var(--secondary); color: var(--text); }
    .continue { background: var(--accent); color: white; }
    .continue:hover { background: var(--accent-strong); }
    .remove { color: var(--danger); }
    code { font: 12.5px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }

    .spinner {
      width: 22px;
      height: 22px;
      border: 3px solid var(--border);
      border-top-color: var(--accent);
      border-radius: 50%;
      animation: spin 0.85s linear infinite;
      flex-shrink: 0;
    }
    @keyframes spin { to { transform: rotate(360deg); } }

    .log-panel {
      margin: 0;
      background: var(--log-bg);
      color: var(--log-fg);
      border-radius: 14px;
      padding: 14px 16px;
      font: 12px/1.55 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      white-space: pre-wrap;
      word-break: break-word;
      height: min(56vh, 520px);
      overflow: auto;
      border: 1px solid var(--border);
    }
    .log-panel .log-line { display: block; }
    .log-panel .log-empty { color: rgba(255,255,255,0.55); }

    .plan-panel {
      margin: 0 0 16px;
      border: 1px solid var(--border);
      border-radius: 16px;
      background: rgba(0, 0, 0, 0.03);
      height: min(52vh, 560px);
      overflow: auto;
    }
    .plan-panel pre {
      margin: 0;
      padding: 18px 20px;
      font: 12.5px/1.55 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      white-space: pre-wrap;
      word-break: break-word;
    }

    @media (max-width: 760px) {
      .row-header { display: none; }
      .row { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body data-phase="editor">
  <div class="shell">
    <section data-section="editor">
      <div class="header">
        <h1>Hosted/Tenant Rancher Setup Preflight</h1>
      </div>
      <div class="body">
        <p class="subtitle" style="margin-top:0">Review the requested Rancher versions for this run. Instance 1 is the host; the remaining instances are tenants. The row count becomes <code>total_rancher_instances</code> automatically. Minimum 2, maximum 4.</p>
        <div class="panel">
          <div class="row-header">
            <div>Instance</div>
            <div>Rancher Version</div>
            <div>Remove</div>
          </div>
          <div class="rows" id="rows"></div>
          <div class="summary">
            <div><strong>Total instances for this run:</strong> <span id="totalInstancesValue"></span></div>
            <div><strong>Config file:</strong> <code>{{.ConfigPath}}</code></div>
            <div><strong>Mode:</strong> auto</div>
            <div>After you submit, this same page will stream resolver logs and then show the full plan for approval.</div>
          </div>
          <div class="error" id="editorErrorBox"></div>
          <div class="status" id="editorStatusBox"></div>
        </div>
      </div>
      <div class="actions">
        <div class="left-actions">
          <button class="secondary" id="addBtn" type="button">Add Instance</button>
        </div>
        <div class="right-actions">
          <button class="secondary" id="editorCancelBtn" type="button">Cancel</button>
          <button class="continue" id="continueBtn" type="button">Resolve Plan</button>
        </div>
      </div>
    </section>

    <section data-section="resolving">
      <div class="header">
        <div class="spinner" aria-hidden="true"></div>
        <div>
          <h1>Resolving Rancher plan</h1>
          <p class="subtitle" style="margin-top:6px">Fetching Helm repos, SUSE support matrix, K3s patch releases, and computing installer SHA256 hashes. Live log stream below.</p>
        </div>
      </div>
      <div class="body">
        <pre class="log-panel" id="logPanel"><span class="log-empty">Waiting for resolver output…</span></pre>
        <div class="error" id="resolvingErrorBox"></div>
      </div>
    </section>

    <section data-section="review">
      <div class="header">
        <h1>Continue with this hosted/tenant Rancher plan?</h1>
      </div>
      <div class="body">
        <p class="subtitle" style="margin-top:0">Review the resolved plan below before continuing. Bootstrap passwords are redacted.</p>
        <div class="plan-panel"><pre id="planPanel"></pre></div>
        <div class="error" id="reviewErrorBox"></div>
      </div>
      <form method="post" action="/respond" class="actions" id="respondForm">
        <input type="hidden" name="token" value="{{.Token}}" />
        <div class="left-actions"></div>
        <div class="right-actions">
          <button type="submit" name="action" value="cancel" class="secondary">Cancel</button>
          <button type="submit" name="action" value="continue" class="continue">Continue</button>
        </div>
      </form>
    </section>

    <section data-section="done">
      <div class="header">
        <h1>You can close this tab</h1>
      </div>
      <div class="body">
        <p class="subtitle" style="margin-top:0">Your response was recorded. The test run is continuing in your terminal.</p>
      </div>
    </section>
  </div>

  <script>
    const token = {{printf "%q" .Token}};
    let versions = {{.InitialVersionsJSON}};
    let submitting = false;

    const rowsEl = document.getElementById('rows');
    const totalInstancesValueEl = document.getElementById('totalInstancesValue');
    const editorErrorBoxEl = document.getElementById('editorErrorBox');
    const editorStatusBoxEl = document.getElementById('editorStatusBox');
    const addBtnEl = document.getElementById('addBtn');
    const editorCancelBtnEl = document.getElementById('editorCancelBtn');
    const continueBtnEl = document.getElementById('continueBtn');
    const logPanelEl = document.getElementById('logPanel');
    const resolvingErrorBoxEl = document.getElementById('resolvingErrorBox');
    const planPanelEl = document.getElementById('planPanel');
    const reviewErrorBoxEl = document.getElementById('reviewErrorBox');
    const respondFormEl = document.getElementById('respondForm');

    function setPhase(phase) {
      document.body.dataset.phase = phase;
    }

    function escapeHtml(value) {
      return String(value)
        .replaceAll('&', '&amp;')
        .replaceAll('<', '&lt;')
        .replaceAll('>', '&gt;')
        .replaceAll('"', '&quot;');
    }

    function instanceRole(index) {
      return index === 0 ? 'Host' : 'Tenant ' + index;
    }

    function renderRows() {
      rowsEl.innerHTML = versions.map((version, index) => {
        const removeDisabled = versions.length <= 2 ? ' disabled' : '';
        return (
          '<div class="row">' +
            '<div class="instance-label">Instance ' + (index + 1) + '<br><span class="instance-role">' + instanceRole(index) + '</span></div>' +
            '<div><input type="text" value="' + escapeHtml(version) + '" data-index="' + index + '" placeholder="2.14.1-alpha3" /></div>' +
            '<div><button class="secondary remove" type="button" data-remove-index="' + index + '"' + removeDisabled + '>Remove</button></div>' +
          '</div>'
        );
      }).join('');
      totalInstancesValueEl.textContent = String(versions.length);
      addBtnEl.disabled = submitting || versions.length >= 4;

      rowsEl.querySelectorAll('input[data-index]').forEach(input => {
        input.addEventListener('input', event => {
          versions[Number(event.target.getAttribute('data-index'))] = event.target.value;
          editorErrorBoxEl.textContent = '';
        });
      });
      rowsEl.querySelectorAll('button[data-remove-index]').forEach(button => {
        button.addEventListener('click', () => {
          if (versions.length <= 2 || submitting) return;
          versions.splice(Number(button.getAttribute('data-remove-index')), 1);
          renderRows();
        });
      });
    }

    function normalizedVersions() {
      return versions.map(version => String(version || '').trim());
    }

    function validateVersions() {
      const trimmed = normalizedVersions();
      if (trimmed.length < 2) return 'At least 2 instances are required (1 host + 1 tenant).';
      if (trimmed.length > 4) return 'No more than 4 instances are supported.';
      for (let i = 0; i < trimmed.length; i++) {
        if (!trimmed[i]) return 'Version for Instance ' + (i + 1) + ' cannot be empty.';
      }
      return '';
    }

    function setSubmittingState(nextSubmitting) {
      submitting = nextSubmitting;
      addBtnEl.disabled = nextSubmitting || versions.length >= 4;
      editorCancelBtnEl.disabled = nextSubmitting;
      continueBtnEl.disabled = nextSubmitting;
      rowsEl.querySelectorAll('input, button[data-remove-index]').forEach(el => {
        el.disabled = nextSubmitting || (el.hasAttribute('data-remove-index') && versions.length <= 2);
      });
    }

    async function submitVersions() {
      const validationError = validateVersions();
      if (validationError) {
        editorErrorBoxEl.textContent = validationError;
        return;
      }
      editorErrorBoxEl.textContent = '';
      editorStatusBoxEl.textContent = 'Saving config and kicking off plan resolution...';
      setSubmittingState(true);

      const response = await fetch('/submit?token=' + encodeURIComponent(token), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ versions: normalizedVersions() })
      });
      if (!response.ok) {
        editorErrorBoxEl.textContent = await response.text();
        editorStatusBoxEl.textContent = '';
        setSubmittingState(false);
        return;
      }
      // SSE will drive the phase transition to "resolving".
    }

    async function cancelEditor() {
      if (submitting) return;
      const form = document.createElement('form');
      form.method = 'post';
      form.action = '/respond';
      form.innerHTML =
        '<input type="hidden" name="token" value="' + escapeHtml(token) + '">' +
        '<input type="hidden" name="action" value="cancel">';
      document.body.appendChild(form);
      form.submit();
    }

    function appendLogLine(line) {
      const empty = logPanelEl.querySelector('.log-empty');
      if (empty) empty.remove();
      const span = document.createElement('span');
      span.className = 'log-line';
      span.textContent = line;
      logPanelEl.appendChild(span);
      logPanelEl.scrollTop = logPanelEl.scrollHeight;
    }

    function connectEventStream() {
      const source = new EventSource('/events?token=' + encodeURIComponent(token));
      source.onmessage = event => {
        let payload;
        try { payload = JSON.parse(event.data); } catch (_) { return; }
        switch (payload.type) {
          case 'phase':
            setPhase(payload.phase);
            if (payload.phase === 'done') source.close();
            break;
          case 'log':
            appendLogLine(payload.line);
            break;
          case 'plan':
            planPanelEl.textContent = payload.plan;
            break;
          case 'error':
            resolvingErrorBoxEl.textContent = payload.error;
            reviewErrorBoxEl.textContent = payload.error;
            break;
        }
      };
      source.onerror = () => {
        // Keep quiet; browser will retry automatically.
      };
    }

    addBtnEl.addEventListener('click', () => {
      if (submitting || versions.length >= 4) return;
      versions.push('');
      renderRows();
    });
    editorCancelBtnEl.addEventListener('click', cancelEditor);
    continueBtnEl.addEventListener('click', submitVersions);

    renderRows();
    connectEventStream();
  </script>
</body>
</html>`
