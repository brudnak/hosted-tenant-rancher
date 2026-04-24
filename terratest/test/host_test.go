package test

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	toolkit "github.com/brudnak/hosted-tenant-rancher/tools"
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
	setupConfig(t)

	resolvedPlans, err := resolveRancherSetup()
	if err != nil {
		t.Fatalf("Rancher setup canceled or failed: %v", err)
	}

	totalInstances := getTotalRancherInstances()
	if totalInstances == 0 {
		t.Fatal("total_rancher_instances must be set")
	}

	helmCommands := viper.GetStringSlice("rancher.helm_commands")
	k3sVersions := viper.GetStringSlice("k3s.versions")

	if err := validateLocalToolingPreflight(helmCommands); err != nil {
		t.Fatalf("local tooling preflight failed: %v", err)
	}
	if err := validateSecretEnvironment(); err != nil {
		t.Fatalf("secret environment preflight failed: %v", err)
	}
	if err := validateHostedConfiguration(totalInstances, helmCommands, resolvedPlans); err != nil {
		t.Fatalf("configuration validation failed: %v", err)
	}

	err = checkS3ObjectExists(tfState)
	if err != nil {
		log.Fatal("Error checking if tfstate exists in s3: ", err)
	}

	createAWSVar()

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
		},
	}

	terraform.InitAndApply(t, terraformOptions)

	flatOutputs := terraform.OutputMap(t, terraformOptions, "flat_outputs")

	var hostConfig toolkit.K3SConfig
	var tenantConfigs []toolkit.K3SConfig

	for i := 0; i < totalInstances; i++ {
		infraServer1IPAddress := flatOutputs[fmt.Sprintf("infra%d_server1_ip", i+1)]
		infraServer2IPAddress := flatOutputs[fmt.Sprintf("infra%d_server2_ip", i+1)]
		infraMysqlEndpoint := flatOutputs[fmt.Sprintf("infra%d_mysql_endpoint", i+1)]
		infraMysqlPassword := flatOutputs[fmt.Sprintf("infra%d_mysql_password", i+1)]
		infraRancherURL := flatOutputs[fmt.Sprintf("infra%d_rancher_url", i+1)]

		if i == 0 {
			hostUrl = infraRancherURL
			hostConfig = toolkit.K3SConfig{
				DBPassword: infraMysqlPassword,
				DBEndpoint: infraMysqlEndpoint,
				RancherURL: infraRancherURL,
				Node1IP:    infraServer1IPAddress,
				Node2IP:    infraServer2IPAddress,
			}
		} else {
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

	log.Printf("Installing K3S on host with version: %s", k3sVersions[0])
	viper.Set("k3s.version", k3sVersions[0])
	tools.K3SHostInstall(hostConfig)

	hostScriptDir := "host-rancher"
	CreateRancherInstallScript(helmCommands[0], hostConfig.RancherURL, hostScriptDir)

	err = saveK3SKubeconfig(hostConfig.Node1IP, hostScriptDir)
	if err != nil {
		t.Fatalf("Failed to save host kubeconfig: %v", err)
	}

	log.Println("Installing host Rancher...")
	err = executeInstallScript(hostScriptDir)
	if err != nil {
		t.Fatalf("Failed to execute host install script: %v", err)
	}

	log.Println("Waiting for host Rancher to be stable...")
	err = waitForRancherStable(hostConfig.RancherURL, 10*time.Minute)
	if err != nil {
		t.Fatalf("Host Rancher failed to become stable: %v", err)
	}

	adminPassword = extractBootstrapPassword(helmCommands[0])
	if adminPassword == "" {
		adminPassword = "admin"
	}

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

	log.Printf("Starting Phase 1: K3S installation and tenant imports (sequential)")
	configIpsMutex := sync.Mutex{}

	for i, tenantConfig := range tenantConfigs {
		tenantIndex := i + 1
		k3sVersionIndex := i + 1

		log.Printf("Starting K3S installation and import for tenant %d", tenantIndex)

		t.Run(fmt.Sprintf("Tenant%d_Phase1", tenantIndex), func(subT *testing.T) {
			if err := setupTenantPhase1(subT, tenantIndex, k3sVersionIndex, tenantConfig, k3sVersions, &configIpsMutex); err != nil {
				t.Fatalf("Tenant %d phase 1 setup failed: %v", tenantIndex, err)
			}
		})

		log.Printf("Tenant %d Phase 1 completed successfully", tenantIndex)
	}

	log.Printf("All tenants successfully imported and Active in host Rancher")

	var phase2WG sync.WaitGroup
	var phase2Err error
	var phase2ErrMutex sync.Mutex

	for i, tenantConfig := range tenantConfigs {
		phase2WG.Add(1)
		tenantIndex := i + 1
		helmCommandIndex := i + 1

		go func(tenantIndex, helmCommandIndex int, tenantConfig toolkit.K3SConfig) {
			defer phase2WG.Done()

			log.Printf("Starting Rancher installation for tenant %d", tenantIndex)

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

	phase2WG.Wait()

	if phase2Err != nil {
		t.Fatalf("Error during parallel tenant phase 2 setup: %v", phase2Err)
	}

	log.Printf("Host Rancher https://%s", hostConfig.RancherURL)
	for i, tenantConfig := range tenantConfigs {
		log.Printf("Tenant Rancher %d https://%s", i+1, tenantConfig.RancherURL)
	}
}

func setupTenantPhase1(t *testing.T, tenantIndex, k3sVersionIndex int, tenantConfig toolkit.K3SConfig, k3sVersions []string, configIpsMutex *sync.Mutex) error {
	log.Printf("Installing K3S on tenant %d with version: %s", tenantIndex, k3sVersions[k3sVersionIndex])
	viper.Set("k3s.version", k3sVersions[k3sVersionIndex])
	tenantIp := tools.K3STenantInstall(tenantConfig)

	configIpsMutex.Lock()
	for len(configIps) < tenantIndex {
		configIps = append(configIps, "")
	}
	configIps[tenantIndex-1] = tenantIp
	configIpsMutex.Unlock()

	currentTenantIndex = tenantIndex
	log.Printf("Importing tenant %d into host Rancher...", tenantIndex)

	tools.SetupImport(hostUrl, adminToken, tenantIndex)

	scriptDir := fmt.Sprintf("tenant-%d-rancher", tenantIndex)
	err := saveK3SKubeconfig(tenantConfig.Node1IP, scriptDir)
	if err != nil {
		return fmt.Errorf("failed to save tenant kubeconfig for import: %w", err)
	}

	err = executeImportScript(scriptDir)
	if err != nil {
		return fmt.Errorf("failed to execute import script: %w", err)
	}

	log.Printf("Waiting for tenant %d cluster to be Active in host Rancher...", tenantIndex)
	err = waitForClusterActive(hostUrl, adminToken, tenantIndex, 10*time.Minute)
	if err != nil {
		return fmt.Errorf("tenant %d cluster failed to become Active: %w", tenantIndex, err)
	}

	log.Printf("Tenant %d successfully imported and Active in host Rancher", tenantIndex)
	return nil
}

func setupTenantPhase2(t *testing.T, tenantIndex, helmCommandIndex int, tenantConfig toolkit.K3SConfig, helmCommands []string) error {
	tenantScriptDir := fmt.Sprintf("tenant-%d-rancher", tenantIndex)
	CreateRancherInstallScript(helmCommands[helmCommandIndex], tenantConfig.RancherURL, tenantScriptDir)

	err := saveK3SKubeconfig(tenantConfig.Node1IP, tenantScriptDir)
	if err != nil {
		return fmt.Errorf("failed to save tenant kubeconfig: %w", err)
	}

	log.Printf("Installing tenant %d Rancher on Active cluster using Helm command %d...", tenantIndex, helmCommandIndex)
	err = executeInstallScript(tenantScriptDir)
	if err != nil {
		return fmt.Errorf("failed to execute tenant install script: %w", err)
	}

	log.Printf("Waiting for tenant %d Rancher to be stable...", tenantIndex)
	err = waitForRancherStable(tenantConfig.RancherURL, 8*time.Minute)
	if err != nil {
		return fmt.Errorf("tenant %d Rancher failed to become stable: %w", tenantIndex, err)
	}

	log.Printf("Tenant Rancher %d https://%s", tenantIndex, tenantConfig.RancherURL)
	return nil
}

func TestCleanup(t *testing.T) {
	setupConfig(t)
	if err := validateSecretEnvironment(); err != nil {
		t.Fatalf("secret environment preflight failed: %v", err)
	}

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: "../modules/aws",
		NoColor:      true,
	})

	createAWSVar()

	var cleanupEstimate *cleanupCostEstimate
	totalInstances := getTotalRancherInstances()
	if outputs, err := terraform.OutputMapE(t, terraformOptions, "flat_outputs"); err == nil {
		if estimate, estimateErr := estimateCurrentRunCost(totalInstances, outputs); estimateErr != nil {
			log.Printf("[cleanup] Could not estimate EC2/EBS/RDS cost before destroy: %v", estimateErr)
		} else {
			cleanupEstimate = estimate
			logCleanupCostEstimate(estimate)
		}
	} else {
		log.Printf("[cleanup] Could not load terraform outputs before destroy: %v", err)
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

	err := clearS3Bucket(viper.GetString("s3.bucket"))
	if err != nil {
		log.Printf("Error clearing bucket [from func clearS3Bucket]: %v", err)
	}

	if cleanupEstimate != nil {
		log.Printf("[cleanup] Cleanup finished. Final estimated run-cost summary:")
		logCleanupCostEstimateWithPrefix(cleanupEstimate, "[cleanup-summary]")
	}
}

func TestSetupImport(t *testing.T) {
	tenantIndex := currentTenantIndex
	tools.SetupImport(hostUrl, adminToken, tenantIndex)

	scriptDir := fmt.Sprintf("tenant-%d-rancher", tenantIndex)
	err := executeImportScript(scriptDir)
	if err != nil {
		t.Fatalf("Failed to execute import script: %v", err)
	}
}

func saveK3SKubeconfig(nodeIP, scriptDir string) error {
	serverKubeConfig, err := tools.RunCommand("sudo cat /etc/rancher/k3s/k3s.yaml", nodeIP)
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig from node %s: %w", nodeIP, err)
	}

	configIP := fmt.Sprintf("https://%s:6443", nodeIP)
	kubeConf := []byte(serverKubeConfig)
	output := bytes.Replace(kubeConf, []byte("https://127.0.0.1:6443"), []byte(configIP), -1)

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

func waitForRancherAPIReady(rancherURL, adminPassword string, timeout time.Duration) error {
	log.Printf("Waiting for Rancher API to be ready for authentication...")

	start := time.Now()
	maxRetries := int(timeout.Seconds() / 15)

	for i := 0; i < maxRetries; i++ {
		token, err := tools.CreateToken(rancherURL, adminPassword)
		if err == nil && token != "" {
			elapsed := time.Since(start)
			log.Printf("Rancher API is fully ready for authentication after %v", elapsed)
			return nil
		}

		if i%4 == 0 {
			elapsed := time.Since(start)
			log.Printf("Rancher API not ready for auth yet after %v: %v", elapsed, err)
		}

		time.Sleep(15 * time.Second)
	}

	return fmt.Errorf("timeout waiting for Rancher API to be ready for authentication after %v", timeout)
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

func cleanupRancherDirectoriesSafe() {
	var scriptDirs []string

	if _, err := os.Stat("host-rancher"); err == nil {
		scriptDirs = append(scriptDirs, "host-rancher")
	}

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
