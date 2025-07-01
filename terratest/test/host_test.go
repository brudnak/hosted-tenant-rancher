package test

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	toolkit "github.com/brudnak/hosted-tenant-rancher/tools"
	"github.com/brudnak/hosted-tenant-rancher/tools/hcl"
	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/spf13/viper"
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

	// Validate arrays AND helm commands before any infrastructure
	err := validateArrayCountsWithHelm()
	if err != nil {
		log.Fatal("Error with validation: ", err)
	}

	// Validate current IP matches whitelisted IP for SSH access
	err = validateCurrentIP()
	if err != nil {
		log.Fatal("Error with IP validation: ", err)
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

	terraformOptions := &terraform.Options{
		TerraformDir: "../modules/aws",
		NoColor:      true,
		BackendConfig: map[string]interface{}{
			"bucket": viper.GetString("s3.bucket"),
			"key":    tfState,
			"region": viper.GetString("s3.region"),
		},
		Vars: map[string]interface{}{
			"total_rancher_instances": viper.GetInt("total_rancher_instances"),
			// All other variables will come from terraform.tfvars file created by createAWSVar()
		},
	}

	terraform.InitAndApply(t, terraformOptions)

	flatOutputs := terraform.OutputMap(t, terraformOptions, "flat_outputs")
	totalInstances := viper.GetInt("total_rancher_instances")
	helmCommands := viper.GetStringSlice("rancher.helm_commands")
	k3sVersions := viper.GetStringSlice("k3s.versions")

	var hostConfig toolkit.K3SConfig
	var tenantConfigs []toolkit.K3SConfig

	for i := 0; i < totalInstances; i++ {
		// Access outputs from the flat_outputs map
		infraServer1IPAddress := flatOutputs[fmt.Sprintf("infra%d_server1_ip", i+1)]
		infraServer2IPAddress := flatOutputs[fmt.Sprintf("infra%d_server2_ip", i+1)]
		infraMysqlEndpoint := flatOutputs[fmt.Sprintf("infra%d_mysql_endpoint", i+1)]
		infraMysqlPassword := flatOutputs[fmt.Sprintf("infra%d_mysql_password", i+1)]
		infraRancherURL := flatOutputs[fmt.Sprintf("infra%d_rancher_url", i+1)]

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

	// PHASE 1: Install K3S on tenants and import them as plain clusters (SEQUENTIAL)
	log.Printf("Starting Phase 1: K3S installation and tenant imports (sequential)")
	configIpsMutex := sync.Mutex{}

	for i, tenantConfig := range tenantConfigs {
		tenantIndex := i + 1
		k3sVersionIndex := i + 1 // Tenant K3S versions start at index 1

		log.Printf("Starting K3S installation and import for tenant %d", tenantIndex)

		// Create a subtest for this tenant
		t.Run(fmt.Sprintf("Tenant%d_Phase1", tenantIndex), func(subT *testing.T) {
			if err := setupTenantPhase1(subT, tenantIndex, k3sVersionIndex, tenantConfig, k3sVersions, &configIpsMutex); err != nil {
				t.Fatalf("Tenant %d phase 1 setup failed: %v", tenantIndex, err)
			}
		})

		log.Printf("Tenant %d Phase 1 completed successfully", tenantIndex)
	}

	log.Printf("All tenants successfully imported and Active in host Rancher")

	// PHASE 2: Install Rancher on each Active tenant using tenant-specific Helm commands (PARALLEL)
	var phase2WG sync.WaitGroup
	var phase2Err error
	var phase2ErrMutex sync.Mutex

	for i, tenantConfig := range tenantConfigs {
		phase2WG.Add(1)
		tenantIndex := i + 1
		helmCommandIndex := i + 1 // Tenant Helm commands start at index 1

		go func(tenantIndex, helmCommandIndex int, tenantConfig toolkit.K3SConfig) {
			defer phase2WG.Done()

			log.Printf("Starting Rancher installation for tenant %d", tenantIndex)

			// Create a subtest for this tenant
			t.Run(fmt.Sprintf("Tenant%d_Phase2", tenantIndex), func(subT *testing.T) {
				if err := setupTenantPhase2(subT, tenantIndex, helmCommandIndex, tenantConfig, helmCommands); err != nil {
					phase2ErrMutex.Lock()
					phase2Err = fmt.Errorf("tenant %d phase 2 setup failed: %s", tenantIndex, err.Error())
					phase2ErrMutex.Unlock()
					subT.Fail()
				}
			})
		}(tenantIndex, helmCommandIndex, tenantConfig)
	}

	// Wait for all tenant phase 2 setups to complete
	phase2WG.Wait()

	// Check if any errors occurred in phase 2
	if phase2Err != nil {
		t.Fatalf("Error during parallel tenant phase 2 setup: %v", phase2Err)
	}

	log.Printf("Host Rancher https://%s", hostConfig.RancherURL)
	for i, tenantConfig := range tenantConfigs {
		log.Printf("Tenant Rancher %d https://%s", i+1, tenantConfig.RancherURL)
	}
}

// setupTenantPhase1 handles K3S installation and cluster import for a single tenant
func setupTenantPhase1(t *testing.T, tenantIndex, k3sVersionIndex int, tenantConfig toolkit.K3SConfig, k3sVersions []string, configIpsMutex *sync.Mutex) error {
	// Install K3S on tenant using tenant-specific K3S version
	log.Printf("Installing K3S on tenant %d with version: %s", tenantIndex, k3sVersions[k3sVersionIndex])
	viper.Set("k3s.version", k3sVersions[k3sVersionIndex])
	tenantIp := tools.K3STenantInstall(tenantConfig)

	// Thread-safe append to configIps
	configIpsMutex.Lock()
	// Ensure configIps slice is large enough
	for len(configIps) < tenantIndex {
		configIps = append(configIps, "")
	}
	configIps[tenantIndex-1] = tenantIp
	configIpsMutex.Unlock()

	// Import tenant into host Rancher
	currentTenantIndex = tenantIndex
	log.Printf("Importing tenant %d into host Rancher...", tenantIndex)

	// Setup import
	tools.SetupImport(hostUrl, adminToken, tenantIndex)

	scriptDir := fmt.Sprintf("tenant-%d-rancher", tenantIndex)
	err := saveK3SKubeconfig(tenantConfig.Node1IP, scriptDir)
	if err != nil {
		return fmt.Errorf("failed to save tenant kubeconfig for import: %w", err)
	}

	// Execute the import script
	err = executeImportScript(scriptDir)
	if err != nil {
		return fmt.Errorf("failed to execute import script: %w", err)
	}

	// Wait for imported cluster to be Active in host Rancher
	log.Printf("Waiting for tenant %d cluster to be Active in host Rancher...", tenantIndex)
	err = waitForClusterActive(hostUrl, adminToken, tenantIndex, 10*time.Minute)
	if err != nil {
		return fmt.Errorf("tenant %d cluster failed to become Active: %w", tenantIndex, err)
	}

	log.Printf("Tenant %d successfully imported and Active in host Rancher", tenantIndex)
	return nil
}

// setupTenantPhase2 handles Rancher installation on an active tenant cluster
func setupTenantPhase2(t *testing.T, tenantIndex, helmCommandIndex int, tenantConfig toolkit.K3SConfig, helmCommands []string) error {
	// Create and execute tenant Rancher install script
	tenantScriptDir := fmt.Sprintf("tenant-%d-rancher", tenantIndex)
	CreateRancherInstallScript(helmCommands[helmCommandIndex], tenantConfig.RancherURL, tenantScriptDir)

	// Save tenant kubeconfig
	err := saveK3SKubeconfig(tenantConfig.Node1IP, tenantScriptDir)
	if err != nil {
		return fmt.Errorf("failed to save tenant kubeconfig: %w", err)
	}

	// Execute tenant install script
	log.Printf("Installing tenant %d Rancher on Active cluster using Helm command %d...", tenantIndex, helmCommandIndex)
	err = executeInstallScript(tenantScriptDir)
	if err != nil {
		return fmt.Errorf("failed to execute tenant install script: %w", err)
	}

	// Wait for tenant Rancher to be stable
	log.Printf("Waiting for tenant %d Rancher to be stable...", tenantIndex)
	err = waitForRancherStable(tenantConfig.RancherURL, 8*time.Minute)
	if err != nil {
		return fmt.Errorf("tenant %d Rancher failed to become stable: %w", tenantIndex, err)
	}

	log.Printf("Tenant Rancher %d https://%s", tenantIndex, tenantConfig.RancherURL)
	return nil
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
		"../modules/aws/.terraform.lock.hcl",
		"../modules/aws/" + tfState,
		"../modules/aws/" + tfStateBackup,
		"../modules/aws/" + tfVars,
	}

	folderPaths := []string{
		"../modules/aws/.terraform",
	}

	cleanupFiles(filePaths...)
	cleanupFolders(folderPaths...)
	cleanupRancherDirectoriesSafe()

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
	tools.SetupImport(hostUrl, adminToken, tenantIndex)

	// Execute the import script instead of running terraform
	scriptDir := fmt.Sprintf("tenant-%d-rancher", tenantIndex)
	err := executeImportScript(scriptDir)
	if err != nil {
		t.Fatalf("Failed to execute import script: %v", err)
	}
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

func writeFile(path string, data []byte) {
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("Failed to write file %s: %v", path, err)
	}
}

func executeImportScript(scriptDir string) error {
	// Get current working directory
	currentDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Make absolute paths
	absScriptDir := filepath.Join(currentDir, scriptDir)
	scriptPath := filepath.Join(absScriptDir, "import.sh")
	kubeconfigPath := filepath.Join(absScriptDir, "kube_config.yaml")

	// Make sure files exist
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

func cleanupRancherDirectoriesSafe() {
	var scriptDirs []string

	// Check for host-rancher directory
	if _, err := os.Stat("host-rancher"); err == nil {
		scriptDirs = append(scriptDirs, "host-rancher")
	}

	// Find all tenant-*-rancher directories
	tenantDirs, err := filepath.Glob("tenant-*-rancher")
	if err != nil {
		log.Printf("Error finding tenant directories: %v", err)
	} else {
		scriptDirs = append(scriptDirs, tenantDirs...)
	}

	if len(scriptDirs) > 0 {
		log.Printf("Found script directories to clean up: %v", scriptDirs)
		cleanupFolders(scriptDirs...)
	} else {
		log.Println("No rancher script directories found to clean up")
	}
}

// validateArrayCountsWithHelm does array validation plus helm command validation
func validateArrayCountsWithHelm() error {
	// First do the existing array validation
	if err := validateArrayCounts(); err != nil {
		return err
	}

	// Then validate helm commands
	log.Println("🚀 Starting helm command validation...")
	if err := validateHelmCommands(); err != nil {
		return fmt.Errorf("helm validation failed: %w", err)
	}

	return nil
}

// validateHelmCommands validates all helm commands before infrastructure setup
func validateHelmCommands() error {
	helmCommands := viper.GetStringSlice("rancher.helm_commands")

	log.Printf("🔍 Validating %d helm commands...", len(helmCommands))

	for i, helmCommand := range helmCommands {
		log.Printf("Validating helm command %d...", i+1)

		if err := validateHelmSyntax(helmCommand, i+1); err != nil {
			return fmt.Errorf("helm command %d failed validation: %w", i+1, err)
		}
	}

	log.Printf("✅ All helm commands validated successfully!")
	return nil
}

// validateHelmSyntax checks basic helm command structure
func validateHelmSyntax(helmCommand string, index int) error {
	log.Printf("  📋 Checking syntax for command %d", index)

	// Check if it's actually a helm command
	if !strings.Contains(helmCommand, "helm install") && !strings.Contains(helmCommand, "helm upgrade") {
		return fmt.Errorf("command doesn't appear to be a helm install/upgrade command")
	}

	// Check for required flags
	requiredFlags := []string{
		"--set hostname=",
		"--set bootstrapPassword=",
		"--set agentTLSMode=system-store",
		"--set tls=external",
	}

	for _, flag := range requiredFlags {
		if !strings.Contains(helmCommand, flag) {
			return fmt.Errorf("missing required flag: %s", flag)
		}
	}

	// Validate hostname placeholder
	if strings.Contains(helmCommand, "--set hostname=placeholder") {
		log.Printf("  ✅ Found hostname placeholder (will be replaced)")
	} else if strings.Contains(helmCommand, "--set hostname=") {
		log.Printf("  ✅ Found hostname setting")
	}

	log.Printf("  ✅ Syntax validation passed for command %d", index)
	return nil
}

// validateCurrentIP checks if current public IP matches the whitelisted IP
func validateCurrentIP() error {
	log.Println("🌐 Validating current IP address...")

	// Get whitelisted IP from config
	whitelistedIP := viper.GetString("aws.whitelisted_ip")

	// Get current public IP
	currentIP, err := getCurrentPublicIP()
	if err != nil {
		return fmt.Errorf("failed to get current public IP: %w", err)
	}

	log.Printf("Current IP: %s", currentIP)
	log.Printf("Whitelisted IP: %s", whitelistedIP)

	// Compare IPs
	if currentIP != whitelistedIP {
		return fmt.Errorf("current IP (%s) does not match whitelisted IP (%s). Please check your VPN connection", currentIP, whitelistedIP)
	}

	log.Printf("✅ IP validation passed - current IP matches whitelisted IP")
	return nil
}

// getCurrentPublicIP gets the current public IP address
func getCurrentPublicIP() (string, error) {
	// Try multiple services in case one is down
	services := []string{
		"https://ifconfig.me/ip",
		"https://api.ipify.org",
		"https://checkip.amazonaws.com",
		"https://icanhazip.com",
	}

	client := &http.Client{Timeout: 10 * time.Second}

	for _, service := range services {
		resp, err := client.Get(service)
		if err != nil {
			log.Printf("Failed to get IP from %s: %v", service, err)
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode == 200 {
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				continue
			}

			ip := strings.TrimSpace(string(body))
			// Basic IP validation
			if net.ParseIP(ip) != nil {
				return ip, nil
			}
		}
	}

	return "", fmt.Errorf("failed to get current public IP from any service")
}
