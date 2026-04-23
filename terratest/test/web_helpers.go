package test

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
)

func randomConfirmationToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func openBrowser(targetURL string) error {
	var cmd *exec.Cmd

	switch {
	case isCommandAvailable("open"):
		cmd = exec.Command("open", targetURL)
	case isCommandAvailable("xdg-open"):
		cmd = exec.Command("xdg-open", targetURL)
	default:
		return fmt.Errorf("no browser launcher found (tried open, xdg-open)")
	}

	return cmd.Start()
}

func isCommandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func requestFromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sameOriginBrowserRequest(r *http.Request) bool {
	if !sameOriginHeaderHost(r.Header.Get("Origin"), r.Host) {
		return sameOriginHeaderHost(r.Header.Get("Referer"), r.Host)
	}
	return true
}

func sameOriginHeaderHost(rawValue, requestHost string) bool {
	rawValue = strings.TrimSpace(rawValue)
	if rawValue == "" {
		return false
	}
	u, err := url.Parse(rawValue)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, requestHost)
}

func writeJSON(w http.ResponseWriter, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(value)
}
