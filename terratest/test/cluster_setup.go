package test

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func CreateRancherInstallScript(helmCommand, rancherURL, scriptDir string) {
	updatedCommand := strings.Replace(helmCommand, "--set hostname=placeholder",
		fmt.Sprintf("--set hostname=%s", rancherURL), 1)

	installScript := fmt.Sprintf(`#!/bin/bash
set -e

# Set kubeconfig to use our local file
export KUBECONFIG=kube_config.yaml

# Verify kubectl connection
echo "Verifying connection to Kubernetes cluster..."
kubectl cluster-info
if [ $? -ne 0 ]; then
  echo "ERROR: Unable to connect to Kubernetes cluster"
  exit 1
fi

# Create namespace
echo "Creating cattle-system namespace..."
kubectl create namespace cattle-system --dry-run=client -o yaml | kubectl apply -f -

# Install Rancher
echo "Installing Rancher..."
%s

echo "Rancher installation complete!"
echo "Rancher URL: https://%s"
`, updatedCommand, rancherURL)

	currentDir, err := os.Getwd()
	if err != nil {
		log.Printf("Failed to get current directory: %v", err)
		return
	}

	absScriptDir := filepath.Join(currentDir, scriptDir)
	err = os.MkdirAll(absScriptDir, os.ModePerm)
	if err != nil {
		log.Printf("Failed to create script directory: %v", err)
		return
	}

	scriptPath := filepath.Join(absScriptDir, "install.sh")
	writeFile(scriptPath, []byte(installScript))
	os.Chmod(scriptPath, 0755)
	log.Printf("Created install script: %s", scriptPath)
}

func executeInstallScript(scriptDir string) error {
	currentDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	absScriptDir := filepath.Join(currentDir, scriptDir)
	scriptPath := filepath.Join(absScriptDir, "install.sh")
	kubeconfigPath := filepath.Join(absScriptDir, "kube_config.yaml")

	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("install script not found: %s", scriptPath)
	}
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("kubeconfig not found: %s", kubeconfigPath)
	}

	err = os.Chmod(scriptPath, 0755)
	if err != nil {
		return fmt.Errorf("failed to make script executable: %w", err)
	}

	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = absScriptDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("Executing: bash %s", scriptPath)
	log.Printf("Working directory: %s", absScriptDir)

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to execute install script: %w", err)
	}

	log.Printf("Successfully executed install script in %s", absScriptDir)
	return nil
}

func executeImportScript(scriptDir string) error {
	currentDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	absScriptDir := filepath.Join(currentDir, scriptDir)
	scriptPath := filepath.Join(absScriptDir, "import.sh")
	kubeconfigPath := filepath.Join(absScriptDir, "kube_config.yaml")

	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("import script not found: %s", scriptPath)
	}
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("kubeconfig not found: %s", kubeconfigPath)
	}

	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = absScriptDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("Executing import script: bash %s", scriptPath)
	log.Printf("Working directory: %s", absScriptDir)

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to execute import script: %w", err)
	}

	log.Printf("Successfully executed import script in %s", absScriptDir)
	return nil
}

func extractBootstrapPassword(helmCommand string) string {
	lines := strings.Split(helmCommand, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "--set bootstrapPassword=") {
			parts := strings.Split(line, "--set bootstrapPassword=")
			if len(parts) > 1 {
				password := strings.Fields(parts[1])[0]
				password = strings.Trim(password, " \\")
				return password
			}
		}
	}
	return ""
}

func waitForRancherStable(rancherURL string, timeout time.Duration) error {
	maxRetries := int(timeout.Seconds() / 20)
	totalMaxTime := 30*time.Second + timeout

	log.Printf("Waiting for Rancher at https://%s to become stable...", rancherURL)
	log.Printf("Will check every 20s for up to %d attempts over %v total", maxRetries, totalMaxTime)

	time.Sleep(30 * time.Second)

	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	start := time.Now()

	for i := 0; i < maxRetries; i++ {
		resp, err := client.Get(fmt.Sprintf("https://%s", rancherURL))
		if err == nil {
			validCodes := []int{200, 302, 401, 403, 404}
			isValidCode := false
			for _, code := range validCodes {
				if resp.StatusCode == code {
					isValidCode = true
					break
				}
			}

			if isValidCode {
				resp.Body.Close()

				apiResp, apiErr := client.Get(fmt.Sprintf("https://%s/v3", rancherURL))
				if apiErr == nil {
					apiValidCode := false
					for _, code := range validCodes {
						if apiResp.StatusCode == code {
							apiValidCode = true
							break
						}
					}

					if apiValidCode {
						apiResp.Body.Close()
						elapsed := time.Since(start)
						log.Printf("Rancher is responding to HTTP requests after %v (status: %d, api status: %d)",
							elapsed, resp.StatusCode, apiResp.StatusCode)

						log.Println("Waiting additional 30s for internal services to stabilize...")
						time.Sleep(30 * time.Second)
						return nil
					}
				}
				if apiResp != nil {
					apiResp.Body.Close()
				}
			}
		}

		if resp != nil {
			resp.Body.Close()
		}

		if i%3 == 0 {
			elapsed := time.Since(start)
			if err != nil {
				log.Printf("Rancher not ready yet after %v (attempt %d/%d, connection error: %v), continuing to wait...", elapsed, i+1, maxRetries, err)
			} else if resp != nil {
				log.Printf("Rancher not ready yet after %v (attempt %d/%d, HTTP %d), continuing to wait...", elapsed, i+1, maxRetries, resp.StatusCode)
			} else {
				log.Printf("Rancher not ready yet after %v (attempt %d/%d), continuing to wait...", elapsed, i+1, maxRetries)
			}
		}

		time.Sleep(20 * time.Second)
	}

	return fmt.Errorf("timeout waiting for Rancher to become stable after %v (%d attempts)", timeout, maxRetries)
}

func waitForClusterActive(hostURL, adminToken string, tenantIndex int, timeout time.Duration) error {
	log.Printf("Waiting for tenant-%d cluster to be Active in host Rancher...", tenantIndex)

	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	clusterName := fmt.Sprintf("imported-tenant-%d", tenantIndex)
	start := time.Now()
	maxRetries := int(timeout.Seconds() / 15)

	for i := 0; i < maxRetries; i++ {
		clustersURL := fmt.Sprintf("https://%s/v1/provisioning.cattle.io.clusters", hostURL)
		req, err := http.NewRequest("GET", clustersURL, nil)
		if err != nil {
			log.Printf("Error creating request: %v", err)
			time.Sleep(15 * time.Second)
			continue
		}

		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", adminToken))
		resp, err := client.Do(req)
		if err != nil {
			if i%4 == 0 {
				elapsed := time.Since(start)
				log.Printf("Error checking cluster status after %v: %v", elapsed, err)
			}
			time.Sleep(15 * time.Second)
			continue
		}

		if resp.StatusCode != 200 {
			resp.Body.Close()
			if i%4 == 0 {
				elapsed := time.Since(start)
				log.Printf("API returned status %d after %v", resp.StatusCode, elapsed)
			}
			time.Sleep(15 * time.Second)
			continue
		}

		var clusterResponse struct {
			Data []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Phase string `json:"phase"`
					Ready bool   `json:"ready"`
				} `json:"status"`
			} `json:"data"`
		}

		err = json.NewDecoder(resp.Body).Decode(&clusterResponse)
		resp.Body.Close()

		if err != nil {
			if i%4 == 0 {
				elapsed := time.Since(start)
				log.Printf("Error parsing cluster response after %v: %v", elapsed, err)
			}
			time.Sleep(15 * time.Second)
			continue
		}

		for _, cluster := range clusterResponse.Data {
			if cluster.Metadata.Name == clusterName {
				log.Printf("Found cluster %s with phase: %s, ready: %t", clusterName, cluster.Status.Phase, cluster.Status.Ready)

				if cluster.Status.Phase == "Active" || cluster.Status.Ready {
					elapsed := time.Since(start)
					log.Printf("Cluster %s is Active after %v", clusterName, elapsed)
					return nil
				}

				if i%4 == 0 {
					elapsed := time.Since(start)
					log.Printf("Cluster %s not ready yet after %v (phase: %s, ready: %t)", clusterName, elapsed, cluster.Status.Phase, cluster.Status.Ready)
				}
				break
			}
		}

		time.Sleep(15 * time.Second)
	}

	return fmt.Errorf("timeout waiting for cluster %s to become Active after %v", clusterName, timeout)
}

func writeFile(path string, data []byte) {
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("Failed to write file %s: %v", path, err)
	}
}
