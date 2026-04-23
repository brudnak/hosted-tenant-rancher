package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

func maybeEditAutoModePreflight() error {
	mode := strings.ToLower(strings.TrimSpace(viper.GetString("rancher.mode")))
	if mode != "auto" {
		return nil
	}
	if viper.GetBool("rancher.auto_approve") {
		return nil
	}

	configPath := strings.TrimSpace(viper.ConfigFileUsed())
	if configPath == "" {
		return fmt.Errorf("failed to determine tool-config.yml path for preflight editor")
	}

	versions := currentPreflightVersions()
	for len(versions) < 2 {
		versions = append(versions, "")
	}

	if err := editAutoModePreflightWithBrowser(configPath, versions); err != nil {
		return fmt.Errorf("auto-mode preflight editor failed: %w", err)
	}

	return nil
}

func currentPreflightVersions() []string {
	requestedVersions := viper.GetStringSlice("rancher.versions")
	if len(requestedVersions) > 0 {
		versions := make([]string, 0, len(requestedVersions))
		for _, version := range requestedVersions {
			versions = append(versions, normalizeVersionInput(version))
		}
		return versions
	}

	if singleVersion := normalizeVersionInput(viper.GetString("rancher.version")); singleVersion != "" {
		return []string{singleVersion, ""}
	}

	totalInstances := getTotalRancherInstances()
	if totalInstances < 2 {
		totalInstances = 2
	}
	if totalInstances > 4 {
		totalInstances = 4
	}

	return make([]string, totalInstances)
}

func editAutoModePreflightWithBrowser(configPath string, versions []string) error {
	token, err := randomConfirmationToken()
	if err != nil {
		return fmt.Errorf("failed to create preflight editor token: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to start preflight editor listener: %w", err)
	}

	serverErrCh := make(chan error, 1)
	resultCh := make(chan error, 1)

	initialVersionsJSON, err := json.Marshal(versions)
	if err != nil {
		return fmt.Errorf("failed to serialize preflight versions: %w", err)
	}

	pageTemplate := template.Must(template.New("preflight-editor").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Hosted/Tenant Rancher Setup Preflight</title>
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
    .body {
      padding: 0 20px 20px;
    }
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
    .rows {
      display: grid;
      gap: 10px;
    }
    .row {
      padding: 10px 6px;
      border-top: 1px solid var(--border);
    }
    .row:first-child {
      border-top: 0;
    }
    .instance-label {
      font-weight: 800;
    }
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
    .summary strong {
      color: var(--text);
    }
    .error {
      margin-top: 14px;
      min-height: 1.4em;
      color: var(--danger);
      font-weight: 600;
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
    button:disabled {
      opacity: 0.6;
      cursor: default;
      transform: none;
    }
    .secondary {
      background: var(--secondary);
      color: var(--text);
    }
    .continue {
      background: var(--accent);
      color: white;
    }
    .continue:hover {
      background: var(--accent-strong);
    }
    .remove {
      color: var(--danger);
    }
    code {
      font: 12.5px/1.5 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
    }
    @media (max-width: 760px) {
      .row-header { display: none; }
      .row { grid-template-columns: 1fr; }
    }
  </style>
</head>
<body>
  <div class="shell">
    <div class="header">
      <h1>Hosted/Tenant Rancher Setup Preflight</h1>
      <p class="subtitle">Review the requested Rancher versions for this run. Instance 1 is the host; the remaining instances are tenants. The row count becomes <code>total_rancher_instances</code> automatically. Minimum 2, maximum 4.</p>
    </div>
    <div class="body">
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
          <div>This screen edits only <code>rancher.versions</code> and <code>total_rancher_instances</code>. The next step shows the resolved chart, image, and K3s plan.</div>
        </div>
        <div class="error" id="errorBox"></div>
        <div class="status" id="statusBox"></div>
      </div>
    </div>
    <div class="actions">
      <div class="left-actions">
        <button class="secondary" id="addBtn" type="button">Add Instance</button>
      </div>
      <div class="right-actions">
        <button class="secondary" id="cancelBtn" type="button">Cancel</button>
        <button class="continue" id="continueBtn" type="button">Continue to Plan</button>
      </div>
    </div>
  </div>
  <script>
    const token = {{printf "%q" .Token}};
    let versions = {{.InitialVersionsJSON}};
    let saving = false;
    const rowsEl = document.getElementById('rows');
    const totalInstancesValueEl = document.getElementById('totalInstancesValue');
    const errorBoxEl = document.getElementById('errorBox');
    const statusBoxEl = document.getElementById('statusBox');
    const addBtnEl = document.getElementById('addBtn');
    const cancelBtnEl = document.getElementById('cancelBtn');
    const continueBtnEl = document.getElementById('continueBtn');

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
      addBtnEl.disabled = saving || versions.length >= 4;

      rowsEl.querySelectorAll('input[data-index]').forEach(input => {
        input.addEventListener('input', event => {
          const index = Number(event.target.getAttribute('data-index'));
          versions[index] = event.target.value;
          errorBoxEl.textContent = '';
        });
      });

      rowsEl.querySelectorAll('button[data-remove-index]').forEach(button => {
        button.addEventListener('click', () => {
          if (versions.length <= 2 || saving) {
            return;
          }
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
      if (trimmed.length < 2) {
        return 'At least 2 instances are required (1 host + 1 tenant).';
      }
      if (trimmed.length > 4) {
        return 'No more than 4 instances are supported.';
      }
      for (let i = 0; i < trimmed.length; i++) {
        if (!trimmed[i]) {
          return 'Version for Instance ' + (i + 1) + ' cannot be empty.';
        }
      }
      return '';
    }

    function setSavingState(nextSaving) {
      saving = nextSaving;
      addBtnEl.disabled = nextSaving || versions.length >= 4;
      cancelBtnEl.disabled = nextSaving;
      continueBtnEl.disabled = nextSaving;
      rowsEl.querySelectorAll('input, button[data-remove-index]').forEach(el => {
        el.disabled = nextSaving || (el.hasAttribute('data-remove-index') && versions.length <= 2);
      });
    }

    async function continueToPlan() {
      const validationError = validateVersions();
      if (validationError) {
        errorBoxEl.textContent = validationError;
        return;
      }

      errorBoxEl.textContent = '';
      statusBoxEl.textContent = 'Saving config and resolving Rancher plans...';
      setSavingState(true);

      const response = await fetch('/submit?token=' + encodeURIComponent(token), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ versions: normalizedVersions() })
      });
      if (!response.ok) {
        errorBoxEl.textContent = await response.text();
        statusBoxEl.textContent = '';
        setSavingState(false);
        return;
      }

      document.body.innerHTML = '<div style="min-height:100vh;display:grid;place-items:center;font-family:ui-sans-serif,-apple-system,BlinkMacSystemFont,Segoe UI,sans-serif;background:inherit;color:inherit;padding:24px;"><div style="max-width:720px;text-align:center;"><h1 style="margin:0 0 12px;">Config saved</h1><p style="margin:0;color:inherit;opacity:.82;">Continuing to the resolved Rancher plan...</p></div></div>';
    }

    async function cancel() {
      if (saving) {
        return;
      }
      await fetch('/cancel?token=' + encodeURIComponent(token), { method: 'POST' });
      document.body.innerHTML = '<div style="min-height:100vh;display:grid;place-items:center;font-family:ui-sans-serif,-apple-system,BlinkMacSystemFont,Segoe UI,sans-serif;background:inherit;color:inherit;padding:24px;"><div style="max-width:720px;text-align:center;"><h1 style="margin:0 0 12px;">Canceled</h1><p style="margin:0;color:inherit;opacity:.82;">The setup run was canceled before plan resolution.</p></div></div>';
    }

    addBtnEl.addEventListener('click', () => {
      if (saving || versions.length >= 4) {
        return;
      }
      versions.push('');
      renderRows();
    });
    cancelBtnEl.addEventListener('click', cancel);
    continueBtnEl.addEventListener('click', continueToPlan);

    renderRows();
  </script>
</body>
</html>`))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.URL.Query().Get("token")) != token {
			http.Error(w, "invalid preflight editor token", http.StatusForbidden)
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
			Token:               token,
			ConfigPath:          configPath,
			InitialVersionsJSON: template.JS(string(initialVersionsJSON)),
		})
	})
	mux.HandleFunc("/submit", func(w http.ResponseWriter, r *http.Request) {
		if !preflightEditorAuthorized(r, token) {
			http.Error(w, "invalid preflight editor token", http.StatusForbidden)
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

		if err := updateAutoModeConfigFile(configPath, normalizedVersions); err != nil {
			http.Error(w, fmt.Sprintf("failed to update tool-config.yml: %v", err), http.StatusInternalServerError)
			return
		}

		writeJSON(w, map[string]string{"status": "saved"})
		select {
		case resultCh <- nil:
		default:
		}
	})
	mux.HandleFunc("/cancel", func(w http.ResponseWriter, r *http.Request) {
		if !preflightEditorAuthorized(r, token) {
			http.Error(w, "invalid preflight editor token", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		writeJSON(w, map[string]string{"status": "canceled"})
		select {
		case resultCh <- fmt.Errorf("user canceled Rancher setup preflight editor"):
		default:
		}
	})

	server := &http.Server{Handler: mux}
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
			serverErrCh <- serveErr
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	editorURL := fmt.Sprintf("http://%s/?token=%s", listener.Addr().String(), token)
	if err := openBrowser(editorURL); err != nil {
		return fmt.Errorf("failed to open preflight editor page: %w", err)
	}

	select {
	case resultErr := <-resultCh:
		return resultErr
	case serveErr := <-serverErrCh:
		return fmt.Errorf("preflight editor server failed: %w", serveErr)
	case <-time.After(30 * time.Minute):
		return fmt.Errorf("timed out waiting for preflight editor response")
	}
}

func preflightEditorAuthorized(r *http.Request, token string) bool {
	if strings.TrimSpace(r.URL.Query().Get("token")) == token {
		return true
	}
	return requestFromLoopback(r) && sameOriginBrowserRequest(r)
}

func normalizePreflightVersions(versions []string) ([]string, error) {
	if len(versions) < 2 {
		return nil, fmt.Errorf("at least 2 Rancher versions are required (1 host + 1 tenant)")
	}
	if len(versions) > 4 {
		return nil, fmt.Errorf("no more than 4 Rancher versions are supported")
	}

	normalized := make([]string, 0, len(versions))
	for i, version := range versions {
		normalizedVersion := normalizeVersionInput(version)
		if normalizedVersion == "" {
			return nil, fmt.Errorf("version for instance %d cannot be empty", i+1)
		}
		normalized = append(normalized, normalizedVersion)
	}

	return normalized, nil
}

func updateAutoModeConfigFile(configPath string, versions []string) error {
	normalizedVersions, err := normalizePreflightVersions(versions)
	if err != nil {
		return err
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}
	if len(document.Content) == 0 {
		return fmt.Errorf("config file is empty")
	}

	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("config root must be a YAML mapping")
	}

	rancherNode := ensureMappingValue(root, "rancher")
	setStringSequenceValue(rancherNode, "versions", normalizedVersions)
	deleteMappingKey(rancherNode, "version")
	setIntValue(root, "total_rancher_instances", len(normalizedVersions))
	deleteMappingKey(root, "total_has")

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return fmt.Errorf("failed to serialize config file: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return fmt.Errorf("failed to finalize config file: %w", err)
	}

	if err := os.WriteFile(configPath, output.Bytes(), 0o644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	viper.Set("rancher.versions", normalizedVersions)
	viper.Set("total_rancher_instances", len(normalizedVersions))
	viper.Set("rancher.version", "")

	return nil
}

func ensureMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if value := mappingValue(mapping, key); value != nil {
		if value.Kind != yaml.MappingNode {
			value.Kind = yaml.MappingNode
			value.Tag = "!!map"
			value.Style = 0
			value.Content = nil
		}
		return value
	}

	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valueNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content, keyNode, valueNode)
	return valueNode
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}

	return nil
}

func deleteMappingKey(mapping *yaml.Node, key string) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return
	}

	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

func setStringSequenceValue(mapping *yaml.Node, key string, values []string) {
	sequenceNode := mappingValue(mapping, key)
	if sequenceNode == nil {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{},
		)
		sequenceNode = mapping.Content[len(mapping.Content)-1]
	}

	sequenceNode.Kind = yaml.SequenceNode
	sequenceNode.Tag = "!!seq"
	sequenceNode.Style = 0
	sequenceNode.Content = make([]*yaml.Node, 0, len(values))
	for _, value := range values {
		sequenceNode.Content = append(sequenceNode.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Style: yaml.DoubleQuotedStyle,
			Value: value,
		})
	}
}

func setIntValue(mapping *yaml.Node, key string, value int) {
	valueNode := mappingValue(mapping, key)
	if valueNode == nil {
		mapping.Content = append(mapping.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			&yaml.Node{},
		)
		valueNode = mapping.Content[len(mapping.Content)-1]
	}

	valueNode.Kind = yaml.ScalarNode
	valueNode.Tag = "!!int"
	valueNode.Style = 0
	valueNode.Value = fmt.Sprintf("%d", value)
}
