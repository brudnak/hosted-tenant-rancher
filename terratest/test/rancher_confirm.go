package test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/viper"
)

func confirmResolvedPlans(plans []*RancherResolvedPlan) error {
	if len(plans) == 0 || plans[0] == nil || plans[0].Mode == "manual" {
		return nil
	}

	logResolvedPlans(plans)

	if viper.GetBool("rancher.auto_approve") {
		log.Printf("[resolver] Auto-approve enabled, continuing without prompt")
		return nil
	}

	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer tty.Close()
		if _, err := fmt.Fprint(tty, "Continue with this hosted/tenant Rancher plan? [y/N]: "); err != nil {
			return err
		}
		reader := bufio.NewReader(tty)
		response, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if isAffirmative(response) {
			return nil
		}
		return fmt.Errorf("user canceled resolved Rancher plans")
	}

	stdinInfo, err := os.Stdin.Stat()
	if err != nil {
		return fmt.Errorf("failed to inspect stdin for confirmation prompt: %w", err)
	}
	if stdinInfo.Mode()&os.ModeCharDevice == 0 {
		approved, err := confirmResolvedPlansWithBrowserDialog(buildResolvedPlansDialogMessage(plans))
		if err == nil {
			if approved {
				log.Printf("[resolver] User approved resolved Rancher plans")
				return nil
			}
			return fmt.Errorf("user canceled resolved Rancher plans")
		}
		return fmt.Errorf("failed to open browser confirmation page: %w; set rancher.auto_approve=true to skip confirmation", err)
	}

	fmt.Print("Continue with this hosted/tenant Rancher plan? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		approved, dialogErr := confirmResolvedPlansWithBrowserDialog(buildResolvedPlansDialogMessage(plans))
		if dialogErr == nil {
			if approved {
				log.Printf("[resolver] User approved resolved Rancher plans")
				return nil
			}
			return fmt.Errorf("user canceled resolved Rancher plans")
		}
		return fmt.Errorf("failed to read confirmation response and failed to open browser confirmation page: %w (browser error: %v)", err, dialogErr)
	}
	if isAffirmative(response) {
		return nil
	}

	return fmt.Errorf("user canceled resolved Rancher plans")
}

func isAffirmative(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "y", "yes", "continue":
		return true
	default:
		return false
	}
}

func confirmResolvedPlansWithBrowserDialog(planMessage string) (bool, error) {
	token, err := randomConfirmationToken()
	if err != nil {
		return false, fmt.Errorf("failed to create browser confirmation token: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return false, fmt.Errorf("failed to start browser confirmation listener: %w", err)
	}

	resultCh := make(chan bool, 1)
	serverErrCh := make(chan error, 1)
	pageTemplate := template.Must(template.New("confirmation-dialog").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Hosted/Tenant Rancher Plan Confirmation</title>
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
      --cancel: #6b5b4d;
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
        --cancel: #9e8d7d;
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
      border-radius: 20px;
      box-shadow: var(--shadow);
      overflow: hidden;
      backdrop-filter: blur(14px);
    }
    .header {
      padding: 24px 28px 12px;
    }
    h1 {
      margin: 0;
      font-size: clamp(1.4rem, 2.2vw, 2rem);
      line-height: 1.15;
    }
    .subtitle {
      margin: 10px 0 0;
      color: var(--muted);
      font-size: 0.98rem;
    }
    .plan {
      margin: 0 20px;
      border: 1px solid var(--border);
      border-radius: 16px;
      background: rgba(0, 0, 0, 0.03);
      height: min(62vh, 720px);
      overflow: auto;
    }
    pre {
      margin: 0;
      padding: 18px 20px 24px;
      font: 12.5px/1.55 ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      white-space: pre-wrap;
      word-break: break-word;
    }
    .actions {
      display: flex;
      justify-content: flex-end;
      gap: 12px;
      padding: 18px 20px 22px;
    }
    button {
      appearance: none;
      border: 0;
      border-radius: 999px;
      padding: 11px 18px;
      font: inherit;
      font-weight: 600;
      cursor: pointer;
      transition: transform 120ms ease, opacity 120ms ease, background 120ms ease;
    }
    button:hover { transform: translateY(-1px); }
    button:active { transform: translateY(0); }
    .cancel {
      background: rgba(127, 108, 91, 0.14);
      color: var(--cancel);
    }
    .continue {
      background: var(--accent);
      color: white;
    }
    .continue:hover {
      background: var(--accent-strong);
    }
  </style>
</head>
<body>
  <div class="shell">
    <div class="header">
      <h1>Continue with this hosted/tenant Rancher plan?</h1>
      <p class="subtitle">Review the resolved plan below before continuing.</p>
    </div>
    <div class="plan">
      <pre>{{.PlanMessage}}</pre>
    </div>
    <form method="post" action="/respond" class="actions">
      <input type="hidden" name="token" value="{{.Token}}" />
      <button type="submit" name="action" value="cancel" class="cancel">Cancel</button>
      <button type="submit" name="action" value="continue" class="continue">Continue</button>
    </form>
  </div>
</body>
</html>`))

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != token {
			http.Error(w, "invalid confirmation token", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = pageTemplate.Execute(w, struct {
			Token       string
			PlanMessage string
		}{
			Token:       token,
			PlanMessage: planMessage,
		})
	})

	mux.HandleFunc("/respond", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "failed to read response", http.StatusBadRequest)
			return
		}
		if r.FormValue("token") != token {
			http.Error(w, "invalid confirmation token", http.StatusForbidden)
			return
		}

		approved := r.FormValue("action") == "continue"
		select {
		case resultCh <- approved:
		default:
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>Rancher Plan Confirmation</title><script>window.close();</script><style>body{font-family:ui-sans-serif,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;padding:32px;background:#f6f1e8;color:#1d1a16}a{color:#1e6a52}</style></head><body><p>Your response was recorded. You can close this tab.</p></body></html>`)
	})

	server := &http.Server{Handler: mux}
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

	confirmURL := fmt.Sprintf("http://%s/?token=%s", listener.Addr().String(), token)
	if err := openBrowser(confirmURL); err != nil {
		return false, fmt.Errorf("failed to open browser confirmation page: %w", err)
	}

	log.Printf("[resolver] Opened browser confirmation page at %s", confirmURL)

	select {
	case approved := <-resultCh:
		return approved, nil
	case serveErr := <-serverErrCh:
		return false, fmt.Errorf("browser confirmation server failed: %w", serveErr)
	case <-time.After(15 * time.Minute):
		return false, fmt.Errorf("timed out waiting for browser confirmation response")
	}
}

func buildResolvedPlansDialogMessage(plans []*RancherResolvedPlan) string {
	sections := []string{"Continue with this hosted/tenant Rancher plan?"}

	for i, plan := range plans {
		if plan == nil {
			continue
		}

		section := []string{fmt.Sprintf("Instance %d", i+1)}
		if plan.RequestedVersion != "" {
			section = append(section, "Requested Rancher: "+plan.RequestedVersion)
		}
		if plan.ChartRepoAlias != "" {
			section = append(section, fmt.Sprintf("Selected chart: %s/rancher@%s", plan.ChartRepoAlias, plan.ChartVersion))
		}
		if plan.RecommendedK3S != "" {
			section = append(section, "Resolved K3s/K8s: "+plan.RecommendedK3S)
		}
		for j, helmCommand := range plan.HelmCommands {
			section = append(section, fmt.Sprintf("Helm command %d:", j+1), sanitizeHelmCommand(helmCommand))
		}
		sections = append(sections, strings.Join(section, "\n"))
	}

	return strings.Join(sections, "\n\n")
}

func logResolvedPlans(plans []*RancherResolvedPlan) {
	for i, plan := range plans {
		log.Printf("[resolver] Hosted/Tenant resolution summary for instance %d:", i+1)
		log.Printf("[resolver] Requested Rancher: %s", plan.RequestedVersion)
		log.Printf("[resolver] Resolved chart: %s/rancher@%s", plan.ChartRepoAlias, plan.ChartVersion)
		log.Printf("[resolver] Resolved K3s: %s", plan.RecommendedK3S)
		log.Printf("[resolver] Support matrix: %s", plan.SupportMatrixURL)
		log.Printf("[resolver] Installer SHA256: %s", plan.InstallScriptSHA256)
		if plan.AirgapImageSHA256 != "" {
			log.Printf("[resolver] Airgap SHA256: %s", plan.AirgapImageSHA256)
		}
		for _, explanation := range plan.Explanation {
			log.Printf("[resolver] Reason: %s", explanation)
		}
		for _, helmCommand := range plan.HelmCommands {
			log.Printf("[resolver] Helm command:\n%s", sanitizeHelmCommand(helmCommand))
		}
	}
}

func sanitizeHelmCommand(command string) string {
	pattern := regexp.MustCompile(`bootstrapPassword=[^\s\\]+`)
	return pattern.ReplaceAllString(command, "bootstrapPassword=<redacted>")
}
