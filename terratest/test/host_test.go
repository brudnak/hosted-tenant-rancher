package test

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	toolkit "github.com/brudnak/hosted-tenant-rancher/tools"
	"github.com/brudnak/hosted-tenant-rancher/tools/hcl"
	"github.com/spf13/viper"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/terraform"
)

var adminToken string
var currentTenantIndex int
var hostUrl string
var adminPassword string
var configIps []string
var tools toolkit.Tools

const (
	tfVars        = "terraform.tfvars"
	tfState       = "terraform.tfstate"
	tfStateBackup = "terraform.tfstate.backup"
)

func TestHosted(t *testing.T) {

	err := validateArrayCounts()
	if err != nil {
		log.Fatal("Error with array validation: ", err)
	}

	err = checkS3ObjectExists(tfState)
	if err != nil {
		log.Fatal("Error checking if tfstate exists in s3: ", err)
	}

	createAWSVar()

	err = os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	if err != nil {
		log.Printf("error setting env: %v", err)
	}

	err = os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))
	if err != nil {
		log.Printf("error setting env: %v", err)
	}

	err = hcl.GenerateAWSMainTF(viper.GetInt("total_rancher_instances"))
	if err != nil {
		log.Println(err)
	}

	terraformOptions := &terraform.Options{
		TerraformDir: "../modules/aws",
		NoColor:      true,
		BackendConfig: map[string]interface{}{
			"bucket": viper.GetString("s3.bucket"),
			"key":    tfState,
			"region": viper.GetString("s3.region"),
		},
	}

	terraform.InitAndApply(t, terraformOptions)

	totalInstances := viper.GetInt("total_rancher_instances")
	helmCommands := viper.GetStringSlice("rancher.helm_commands")
	k3sVersions := viper.GetStringSlice("k3s.versions")

	var hostConfig toolkit.K3SConfig
	var tenantConfigs []toolkit.K3SConfig

	for i := 0; i < totalInstances; i++ {
		infraServer1IPAddress := terraform.Output(t, terraformOptions, fmt.Sprintf("infra%d_server1_ip", i+1))
		infraServer2IPAddress := terraform.Output(t, terraformOptions, fmt.Sprintf("infra%d_server2_ip", i+1))
		infraMysqlEndpoint := terraform.Output(t, terraformOptions, fmt.Sprintf("infra%d_mysql_endpoint", i+1))
		infraMysqlPassword := terraform.Output(t, terraformOptions, fmt.Sprintf("infra%d_mysql_password", i+1))
		infraRancherURL := terraform.Output(t, terraformOptions, fmt.Sprintf("infra%d_rancher_url", i+1))

		if i == 0 {
			// Set Host URL
			hostUrl = infraRancherURL
			// Host configuration
			hostConfig = toolkit.K3SConfig{
				DBPassword: infraMysqlPassword,
				DBEndpoint: infraMysqlEndpoint,
				RancherURL: infraRancherURL,
				Node1IP:    infraServer1IPAddress,
				Node2IP:    infraServer2IPAddress,
			}
		} else {
			// Tenant configurations
			tenantConfig := toolkit.K3SConfig{
				DBPassword: infraMysqlPassword,
				DBEndpoint: infraMysqlEndpoint,
				RancherURL: infraRancherURL,
				Node1IP:    infraServer1IPAddress,
				Node2IP:    infraServer2IPAddress,
			}
			tenantConfigs = append(tenantConfigs, tenantConfig)
		}
	}

	err = uploadFolderToS3("../modules/aws")
	if err != nil {
		log.Printf("Error uploading folder [from func uploadFolderToS3]: %v", err)
	}

	// Install K3S on host using host K3S version (index 0)
	log.Printf("Installing K3S on host with version: %s", k3sVersions[0])
	viper.Set("k3s.version", k3sVersions[0])
	tools.K3SHostInstall(hostConfig)

	// Create and execute host Rancher install script using host Helm command (index 0)
	hostScriptDir := "host-rancher"
	CreateRancherInstallScript(helmCommands[0], hostConfig.RancherURL, hostScriptDir)

	// Save host kubeconfig
	err = saveK3SKubeconfig(hostConfig.Node1IP, hostScriptDir)
	if err != nil {
		t.Fatalf("Failed to save host kubeconfig: %v", err)
	}

	// Execute host install script
	log.Println("Installing host Rancher...")
	err = executeInstallScript(hostScriptDir)
	if err != nil {
		t.Fatalf("Failed to execute host install script: %v", err)
	}

	// Wait for host Rancher to be stable before proceeding
	log.Println("Waiting for host Rancher to be stable...")
	err = waitForRancherStable(hostConfig.RancherURL, 10*time.Minute)
	if err != nil {
		t.Fatalf("Host Rancher failed to become stable: %v", err)
	}

	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err = viper.ReadInConfig()
	if err != nil {
		log.Println("error reading config:", err)
	}

	// Get bootstrap password from first helm command
	adminPassword = extractBootstrapPassword(helmCommands[0])
	if adminPassword == "" {
		adminPassword = "admin" // fallback
	}

	// Wait for Rancher API to be ready for authentication
	log.Println("Waiting for Rancher API to be ready for authentication...")
	err = waitForRancherAPIReady(hostUrl, adminPassword, 10*time.Minute)
	if err != nil {
		t.Fatalf("Rancher API failed to become ready: %v", err)
	}

	adminToken, err = tools.CreateToken(hostUrl, adminPassword)
	if err != nil {
		log.Fatal("error creating token:", err)
	}

	err = tools.CallBashScript(hostUrl, adminToken)
	if err != nil {
		log.Println("error calling bash script", err)
	}

	log.Printf("Host Rancher https://%s is ready for tenant imports", hostConfig.RancherURL)

	// PHASE 1: Install K3S on tenants and import them as plain clusters
	for i, tenantConfig := range tenantConfigs {
		tenantIndex := i + 1
		k3sVersionIndex := i + 1 // Tenant K3S versions start at index 1

		err := createTenantDirectories(tenantIndex)
		if err != nil {
			t.Fatalf("failed to create tenant directories: %v", err)
		}

		err = tools.GenerateKubectlTenantConfig(tenantIndex)
		if err != nil {
			t.Fatalf("failed to generate kubectl tenant config: %v", err)
		}

		// Install K3S on tenant using tenant-specific K3S version
		log.Printf("Installing K3S on tenant %d with version: %s", tenantIndex, k3sVersions[k3sVersionIndex])
		viper.Set("k3s.version", k3sVersions[k3sVersionIndex])
		tenantIp := tools.K3STenantInstall(tenantConfig, tenantIndex)
		configIps = append(configIps, tenantIp)

		// Import tenant into host Rancher
		currentTenantIndex = tenantIndex
		log.Printf("Importing tenant %d into host Rancher...", tenantIndex)
		t.Run(fmt.Sprintf("setup rancher import for tenant %d", tenantIndex), func(t *testing.T) {
			TestSetupImport(t)
		})

		// Wait for imported cluster to be Active in host Rancher
		log.Printf("Waiting for tenant %d cluster to be Active in host Rancher...", tenantIndex)
		err = waitForClusterActive(hostUrl, adminToken, tenantIndex, 10*time.Minute)
		if err != nil {
			t.Fatalf("Tenant %d cluster failed to become Active: %v", tenantIndex, err)
		}

		log.Printf("Tenant %d successfully imported and Active in host Rancher", tenantIndex)
	}

	// PHASE 2: Install Rancher on each Active tenant using tenant-specific Helm commands
	for i, tenantConfig := range tenantConfigs {
		tenantIndex := i + 1
		helmCommandIndex := i + 1 // Tenant Helm commands start at index 1

		// Create and execute tenant Rancher install script
		tenantScriptDir := fmt.Sprintf("tenant-%d-rancher", tenantIndex)
		CreateRancherInstallScript(helmCommands[helmCommandIndex], tenantConfig.RancherURL, tenantScriptDir)

		// Save tenant kubeconfig
		err := saveK3SKubeconfig(tenantConfig.Node1IP, tenantScriptDir)
		if err != nil {
			t.Fatalf("Failed to save tenant kubeconfig: %v", err)
		}

		// Execute tenant install script
		log.Printf("Installing tenant %d Rancher on Active cluster using Helm command %d...", tenantIndex, helmCommandIndex)
		err = executeInstallScript(tenantScriptDir)
		if err != nil {
			t.Fatalf("Failed to execute tenant install script: %v", err)
		}

		// Wait for tenant Rancher to be stable
		log.Printf("Waiting for tenant %d Rancher to be stable...", tenantIndex)
		err = waitForRancherStable(tenantConfig.RancherURL, 8*time.Minute)
		if err != nil {
			t.Fatalf("Tenant %d Rancher failed to become stable: %v", tenantIndex, err)
		}

		log.Printf("Tenant Rancher %d https://%s", tenantIndex, tenantConfig.RancherURL)
	}

	log.Printf("Host Rancher https://%s", hostConfig.RancherURL)
	for i, tenantConfig := range tenantConfigs {
		log.Printf("Tenant Rancher %d https://%s", i+1, tenantConfig.RancherURL)
	}
}

func TestCleanup(t *testing.T) {
	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: "../modules/aws",
		NoColor:      true,
	})

	createAWSVar()
	err := os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	if err != nil {
		log.Printf("error setting env: %v", err)
	}

	err = os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))
	if err != nil {
		log.Printf("error setting env: %v", err)
	}

	terraform.Destroy(t, terraformOptions)

	filePaths := []string{
		"../../host.yml",
		"../modules/aws/.terraform.lock.hcl",
		"../modules/aws/" + tfState,
		"../modules/aws/" + tfStateBackup,
		"../modules/aws/" + tfVars,
	}

	folderPaths := []string{
		"../modules/aws/.terraform",
	}

	// Clean up script directories
	scriptDirs := []string{
		"host-rancher",
	}

	totalInstances := viper.GetInt("total_rancher_instances")
	for i := 1; i < totalInstances; i++ {
		scriptDirs = append(scriptDirs, fmt.Sprintf("tenant-%d-rancher", i))
	}

	cleanupFiles(filePaths...)
	cleanupFolders(folderPaths...)
	cleanupFolders(scriptDirs...)

	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")

	err = viper.ReadInConfig()
	if err != nil {
		log.Println("error reading config:", err)
	}

	err = clearS3Bucket(viper.GetString("s3.bucket"))
	if err != nil {
		log.Printf("Error clearing bucket [from func clearS3Bucket]: %v", err)
	}

	err = hcl.CleanupTerraformConfig()
	if err != nil {
		log.Printf("error cleaning up main.tf and dirs: %s", err)
	}
}

func TestSetupImport(t *testing.T) {
	tenantIndex := currentTenantIndex
	configIp := configIps[tenantIndex-1]

	//TODO not needed?
	//time.Sleep(120 * time.Second)
	tools.SetupImport(hostUrl, adminToken, configIp, tenantIndex)

	tenantKubeConfigPath := fmt.Sprintf("../modules/kubectl/tenant-%d/tenant_kube_config.yml", tenantIndex)
	err := os.Setenv("KUBECONFIG", tenantKubeConfigPath)
	if err != nil {
		log.Println("error setting env", err)
	}

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: fmt.Sprintf("../modules/kubectl/tenant-%d", tenantIndex),
		NoColor:      true,
	})
	terraform.InitAndApply(t, terraformOptions)
}

func validateArrayCounts() error {
	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()
	if err != nil {
		return fmt.Errorf("error reading config: %v", err)
	}

	totalInstances := viper.GetInt("total_rancher_instances")
	helmCommands := viper.GetStringSlice("rancher.helm_commands")
	k3sVersions := viper.GetStringSlice("k3s.versions")

	// Validate minimum and maximum instances
	if totalInstances < 2 {
		return fmt.Errorf("total_rancher_instances must be at least 2 (1 host + 1 tenant), got: %d", totalInstances)
	}
	if totalInstances > 4 {
		return fmt.Errorf("total_rancher_instances cannot exceed 4, got: %d", totalInstances)
	}

	// Validate Helm commands count matches total instances
	if len(helmCommands) != totalInstances {
		return fmt.Errorf("number of Helm commands (%d) does not match total_rancher_instances (%d). Please ensure you have exactly %d Helm commands in your configuration",
			len(helmCommands), totalInstances, totalInstances)
	}

	// Validate K3S versions count matches total instances
	if len(k3sVersions) != totalInstances {
		return fmt.Errorf("number of K3S versions (%d) does not match total_rancher_instances (%d). Please ensure you have exactly %d K3S versions in your configuration",
			len(k3sVersions), totalInstances, totalInstances)
	}

	log.Printf("✅ Validation passed: %d instances, %d Helm commands, %d K3S versions", totalInstances, len(helmCommands), len(k3sVersions))
	return nil
}

func CreateRancherInstallScript(helmCommand, rancherURL, scriptDir string) {
	// Replace placeholder hostname with actual URL
	updatedCommand := strings.Replace(helmCommand, "--set hostname=placeholder",
		fmt.Sprintf("--set hostname=%s", rancherURL), 1)

	// If no hostname placeholder found, add it
	if !strings.Contains(helmCommand, "--set hostname=") {
		updatedCommand = strings.TrimSpace(helmCommand) + fmt.Sprintf(" \\\n  --set hostname=%s", rancherURL)
	}

	installScript := fmt.Sprintf(`#!/bin/bash
set -e

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

	// Get current working directory and create absolute path
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

func saveK3SKubeconfig(nodeIP, scriptDir string) error {

	serverKubeConfig, err := tools.RunCommand("sudo cat /etc/rancher/k3s/k3s.yaml", nodeIP)
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig from node %s: %w", nodeIP, err)
	}

	// Replace localhost with node IP
	configIP := fmt.Sprintf("https://%s:6443", nodeIP)
	kubeConf := []byte(serverKubeConfig)
	output := bytes.Replace(kubeConf, []byte("https://127.0.0.1:6443"), []byte(configIP), -1)

	// Get current working directory and create absolute path
	currentDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	absScriptDir := filepath.Join(currentDir, scriptDir)
	err = os.MkdirAll(absScriptDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create script directory: %w", err)
	}

	kubeconfigPath := filepath.Join(absScriptDir, "kube_config.yaml")
	err = os.WriteFile(kubeconfigPath, output, 0644)
	if err != nil {
		return fmt.Errorf("failed to write kubeconfig: %w", err)
	}

	log.Printf("Saved kubeconfig to: %s", kubeconfigPath)
	return nil
}

func executeInstallScript(scriptDir string) error {
	// Get current working directory
	currentDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Make absolute paths
	absScriptDir := filepath.Join(currentDir, scriptDir)
	scriptPath := filepath.Join(absScriptDir, "install.sh")
	kubeconfigPath := filepath.Join(absScriptDir, "kube_config.yaml")

	// Make sure files exist
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		return fmt.Errorf("install script not found: %s", scriptPath)
	}
	if _, err := os.Stat(kubeconfigPath); os.IsNotExist(err) {
		return fmt.Errorf("kubeconfig not found: %s", kubeconfigPath)
	}

	// Make script executable (just in case)
	err = os.Chmod(scriptPath, 0755)
	if err != nil {
		return fmt.Errorf("failed to make script executable: %w", err)
	}

	cmd := exec.Command("bash", scriptPath)
	cmd.Dir = absScriptDir
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", kubeconfigPath))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("Executing: bash %s", scriptPath)
	log.Printf("Working directory: %s", absScriptDir)
	log.Printf("KUBECONFIG: %s", kubeconfigPath)

	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to execute install script: %w", err)
	}

	log.Printf("Successfully executed install script in %s", absScriptDir)
	return nil
}

func extractBootstrapPassword(helmCommand string) string {
	// Extract bootstrap password from helm command
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

// waitForRancherStable checks if Rancher is ready for bootstrap
func waitForRancherStable(rancherURL string, timeout time.Duration) error {
	maxRetries := int(timeout.Seconds() / 20) // Check every 20 seconds
	totalMaxTime := 30*time.Second + timeout  // 30s initial + timeout duration

	log.Printf("Waiting for Rancher at https://%s to become stable...", rancherURL)
	log.Printf("Will check every 20s for up to %d attempts over %v total", maxRetries, totalMaxTime)

	// Wait a bit for initial startup
	time.Sleep(30 * time.Second)

	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	start := time.Now()

	for i := 0; i < maxRetries; i++ {
		// Check if Rancher HTTP endpoint is responding
		resp, err := client.Get(fmt.Sprintf("https://%s", rancherURL))
		if err == nil {
			// Accept various status codes that indicate Rancher is running
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

				// Additional check: try the API endpoint with same logic
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

						// Give it a bit more time for internal startup
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

		if i%3 == 0 { // Log every minute
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

// waitForRancherAPIReady waits for Rancher API to be ready with authentication
func waitForRancherAPIReady(rancherURL, adminPassword string, timeout time.Duration) error {
	log.Printf("Waiting for Rancher API to be ready for authentication...")

	start := time.Now()
	maxRetries := int(timeout.Seconds() / 15) // Check every 15 seconds

	for i := 0; i < maxRetries; i++ {
		// Try to create a token (this validates the bootstrap process is complete)
		token, err := tools.CreateToken(rancherURL, adminPassword)
		if err == nil && token != "" {
			elapsed := time.Since(start)
			log.Printf("Rancher API is fully ready for authentication after %v", elapsed)
			return nil
		}

		if i%4 == 0 { // Log every minute
			elapsed := time.Since(start)
			log.Printf("Rancher API not ready for auth yet after %v: %v", elapsed, err)
		}

		time.Sleep(15 * time.Second)
	}

	return fmt.Errorf("timeout waiting for Rancher API to be ready for authentication after %v", timeout)
}

// waitForClusterActive waits for imported cluster to be Active in host Rancher
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
	maxRetries := int(timeout.Seconds() / 15) // Check every 15 seconds

	for i := 0; i < maxRetries; i++ {
		// Query clusters endpoint
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
			if i%4 == 0 { // Log every minute
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

		// Parse response to check cluster status
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

		// Look for our cluster
		for _, cluster := range clusterResponse.Data {
			if cluster.Metadata.Name == clusterName {
				log.Printf("Found cluster %s with phase: %s, ready: %t", clusterName, cluster.Status.Phase, cluster.Status.Ready)

				// Check if cluster is Active/Ready
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

func cleanupFiles(paths ...string) {
	for _, path := range paths {
		err := tools.RemoveFile(path)
		if err != nil {
			log.Println("error removing file", err)
		}
	}
}

func cleanupFolders(paths ...string) {
	for _, path := range paths {
		err := tools.RemoveFolder(path)
		if err != nil {
			log.Println("error removing folder", err)
		}
	}
}

func createAWSVar() {
	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()
	if err != nil {
		log.Println("error reading config:", err)
	}

	hcl.GenAwsVar(
		viper.GetString("tf_vars.aws_access_key"),
		viper.GetString("tf_vars.aws_secret_key"),
		viper.GetString("tf_vars.aws_prefix"),
		viper.GetString("tf_vars.aws_vpc"),
		viper.GetString("tf_vars.aws_subnet_a"),
		viper.GetString("tf_vars.aws_subnet_b"),
		viper.GetString("tf_vars.aws_subnet_c"),
		viper.GetString("tf_vars.aws_ami"),
		viper.GetString("tf_vars.aws_subnet_id"),
		viper.GetString("tf_vars.aws_security_group_id"),
		viper.GetString("tf_vars.aws_pem_key_name"),
		viper.GetString("tf_vars.aws_rds_password"),
		viper.GetString("tf_vars.aws_route53_fqdn"),
		viper.GetString("tf_vars.aws_ec2_instance_type"),
	)
}

func checkS3ObjectExists(item string) error {
	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()

	err = os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	if err != nil {
		log.Println("Error setting env")
		return err
	}

	err = os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))
	if err != nil {
		log.Println("Error setting env")
		return err
	}

	sess, _ := session.NewSession(&aws.Config{
		Region: aws.String(viper.GetString("s3.region"))},
	)

	bucket := viper.GetString("s3.bucket")

	svc := s3.New(sess)

	_, err = svc.HeadObject(&s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(item)})
	if err != nil {
		// If the error is due to the file not existing, that's fine, and we return nil.
		var aErr awserr.Error
		if errors.As(err, &aErr) {
			switch aErr.Code() {
			case s3.ErrCodeNoSuchKey, "NotFound":
				return nil
			}
		}
		// Otherwise, we return the error as it might be due to a network issue or something else.
		return err
	}

	// If we get to this point, it means the file exists, so we log an error message and exit the program.
	log.Fatalf("A tfstate file already exists in bucket %s. Please clean up the old hosted/tenant environment before creating a new one.", bucket)
	return nil
}

func uploadFolderToS3(folderPath string) error {
	// Initialize viper and set environment variables
	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()
	if err != nil {
		return fmt.Errorf("error reading config: %w", err)
	}

	err = os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	if err != nil {
		return fmt.Errorf("error setting AWS_ACCESS_KEY_ID: %w", err)
	}

	err = os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))
	if err != nil {
		return fmt.Errorf("error setting AWS_SECRET_ACCESS_KEY: %w", err)
	}

	// Create a new AWS session
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(viper.GetString("s3.region")),
	})
	if err != nil {
		return fmt.Errorf("error creating AWS session: %w", err)
	}

	svc := s3.New(sess)

	// Get the bucket name from the config
	bucket := viper.GetString("s3.bucket")

	// Walk through the folder
	err = filepath.Walk(folderPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories, we'll create them implicitly when we upload files
		if info.IsDir() {
			return nil
		}

		// Open the file
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("error opening file %s: %w", path, err)
		}
		defer func(file *os.File) {
			err := file.Close()
			if err != nil {

			}
		}(file)

		// Create the S3 key (path in the bucket)
		key, err := filepath.Rel(folderPath, path)
		if err != nil {
			return fmt.Errorf("error getting relative path: %w", err)
		}
		// Convert Windows path separators to forward slashes for S3
		key = strings.ReplaceAll(key, string(os.PathSeparator), "/")

		// Upload the file to S3
		_, err = svc.PutObject(&s3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   file,
		})
		if err != nil {
			return fmt.Errorf("error uploading file %s: %w", path, err)
		}

		log.Printf("Successfully uploaded %s to %s\n", path, bucket+"/"+key)
		return nil
	})

	if err != nil {
		return fmt.Errorf("error walking through folder: %w", err)
	}

	return nil
}

func clearS3Bucket(bucketName string) error {
	// Initialize viper and set environment variables
	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()
	if err != nil {
		return fmt.Errorf("error reading config: %w", err)
	}

	err = os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	if err != nil {
		return fmt.Errorf("error setting AWS_ACCESS_KEY_ID: %w", err)
	}

	err = os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))
	if err != nil {
		return fmt.Errorf("error setting AWS_SECRET_ACCESS_KEY: %w", err)
	}

	// Create a new AWS session
	sess, err := session.NewSession(&aws.Config{
		Region: aws.String(viper.GetString("s3.region")),
	})
	if err != nil {
		return fmt.Errorf("error creating AWS session: %w", err)
	}

	svc := s3.New(sess)

	// List all objects in the bucket
	err = svc.ListObjectsV2Pages(&s3.ListObjectsV2Input{
		Bucket: aws.String(bucketName),
	}, func(page *s3.ListObjectsV2Output, lastPage bool) bool {
		// Create a list of objects to delete
		var objectsToDelete []*s3.ObjectIdentifier
		for _, obj := range page.Contents {
			objectsToDelete = append(objectsToDelete, &s3.ObjectIdentifier{
				Key: obj.Key,
			})
		}

		// Delete the objects
		if len(objectsToDelete) > 0 {
			_, err := svc.DeleteObjects(&s3.DeleteObjectsInput{
				Bucket: aws.String(bucketName),
				Delete: &s3.Delete{
					Objects: objectsToDelete,
					Quiet:   aws.Bool(false),
				},
			})
			if err != nil {
				fmt.Printf("Error deleting objects: %v\n", err)
				return false
			}
		}

		return true // continue paging
	})

	if err != nil {
		return fmt.Errorf("error clearing bucket: %w", err)
	}

	fmt.Printf("Successfully cleared all contents from bucket: %s\n", bucketName)
	return nil
}

func createTenantDirectories(tenantIndex int) error {
	kubectlDir := fmt.Sprintf("../modules/kubectl/tenant-%d", tenantIndex)

	err := os.MkdirAll(kubectlDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create kubectl directory for tenant %d: %v", tenantIndex, err)
	}

	return nil
}

func writeFile(path string, data []byte) {
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("Failed to write file %s: %v", path, err)
	}
}
