package toolkit

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/spf13/viper"
)

const (
	randomStringSource = "abcdefghijklmnopqrstuvwxyz"
)

var (
	awsClientsMu sync.Mutex
	awsSession   *session.Session
	ec2Client    *ec2.EC2
	ssmClient    *ssm.SSM
	instanceIDs  sync.Map
	ssmOnline    sync.Map
)

type Tools struct{}

type K3SConfig struct {
	DBPassword string
	DBEndpoint string
	RancherURL string
	Node1IP    string
	Node2IP    string
}

func (t *Tools) RandomString(n int) string {
	s, r := make([]rune, n), []rune(randomStringSource)
	for i := range s {
		p, _ := rand.Prime(rand.Reader, len(r))
		x, y := p.Uint64(), uint64(len(r))
		s[i] = r[x%y]
	}
	return string(s)
}

func (t *Tools) WaitForNodeReady(nodeIP string) error {
	timeout := time.After(5 * time.Minute)
	poll := time.Tick(10 * time.Second)

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timed out waiting for node to become ready")
		case <-poll:
			nodeStatus, err := t.RunCommand("sudo systemctl is-active k3s || true", nodeIP)
			if err != nil {
				return fmt.Errorf("failed to check node status: %w", err)
			}

			if strings.TrimSpace(nodeStatus) == "active" {
				return nil
			}
		}
	}
}

func (t *Tools) K3SHostInstall(config K3SConfig) string {
	return t.installK3SCluster(config)
}

func (t *Tools) K3STenantInstall(config K3SConfig) string {
	return t.installK3SCluster(config)
}

func (t *Tools) CreateToken(url string, password string) (string, error) {
	loginPayload := LoginPayload{
		Description:  t.RandomString(6),
		ResponseType: "token",
		Username:     "admin",
		Password:     password,
	}

	loginBody, err := json.Marshal(loginPayload)
	if err != nil {
		return "", err
	}

	adminLogin := fmt.Sprintf("https://%s/v3-public/localProviders/local?action=login", url)
	resp, err := http.Post(adminLogin, "application/json", bytes.NewBuffer(loginBody))
	if err != nil {
		return "", err
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var loginResp LoginResponse
	err = json.Unmarshal(b, &loginResp)
	if err != nil {
		return "", err
	}

	client := &http.Client{
		Timeout: time.Second * 10,
	}

	tokenBody := TokenBody{
		Type:        "token",
		Metadata:    struct{}{},
		Description: "ADMIN_TOKEN",
		TTL:         7776000000,
	}

	jsonTokenBody, err := json.Marshal(tokenBody)
	if err != nil {
		return "", err
	}

	tokenUrl := fmt.Sprintf("https://%s/v3/tokens", url)
	req, err := http.NewRequest("POST", tokenUrl, bytes.NewBuffer(jsonTokenBody))
	if err != nil {
		return "", err
	}

	bearer := fmt.Sprintf("Bearer %s", loginResp.Token)
	req.Header.Set("Authorization", bearer)

	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			// Handle the error if needed
		}
	}(response.Body)

	tokenBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}

	var tokenRes TokenResponse
	err = json.Unmarshal(tokenBytes, &tokenRes)
	if err != nil {
		return "", err
	}

	return tokenRes.Token, nil
}

func (t *Tools) RunCommand(cmd string, pubIP string) (string, error) {
	instanceID, err := getInstanceIDFromIP(pubIP)
	if err != nil {
		return "", fmt.Errorf("failed to resolve instance for %s: %w", pubIP, err)
	}

	if err := waitForSSMAgent(instanceID, 5*time.Minute); err != nil {
		return "", fmt.Errorf("ssm agent not ready for %s (%s): %w", pubIP, instanceID, err)
	}

	output, err := runCommandSSM(cmd, instanceID)
	if err != nil {
		return "", fmt.Errorf("failed to run command via ssm on %s (%s): %w", pubIP, instanceID, err)
	}

	return output, nil
}

func initAWSClients() error {
	awsClientsMu.Lock()
	defer awsClientsMu.Unlock()

	if awsSession != nil && ec2Client != nil && ssmClient != nil {
		return nil
	}

	cfg := aws.NewConfig().WithRegion(resolveAWSRegion())
	accessKey := strings.TrimSpace(os.Getenv("AWS_ACCESS_KEY_ID"))
	secretKey := strings.TrimSpace(os.Getenv("AWS_SECRET_ACCESS_KEY"))
	if accessKey == "" {
		accessKey = viper.GetString("tf_vars.aws_access_key")
	}
	if secretKey == "" {
		secretKey = viper.GetString("tf_vars.aws_secret_key")
	}
	if accessKey != "" && secretKey != "" {
		cfg = cfg.WithCredentials(credentials.NewStaticCredentials(
			accessKey,
			secretKey,
			"",
		))
	}

	sess, err := session.NewSession(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize aws session: %w", err)
	}

	awsSession = sess
	ec2Client = ec2.New(sess)
	ssmClient = ssm.New(sess)

	return nil
}

func resolveAWSRegion() string {
	if region := viper.GetString("tf_vars.aws_region"); region != "" {
		return region
	}
	if region := viper.GetString("s3.region"); region != "" {
		return region
	}

	return "us-east-2"
}

func getInstanceIDFromIP(ip string) (string, error) {
	if err := initAWSClients(); err != nil {
		return "", err
	}

	if cached, ok := instanceIDs.Load(ip); ok {
		return cached.(string), nil
	}

	lookupFilters := [][]*ec2.Filter{
		{
			{
				Name:   aws.String("ip-address"),
				Values: []*string{aws.String(ip)},
			},
			{
				Name:   aws.String("instance-state-name"),
				Values: []*string{aws.String(ec2.InstanceStateNamePending), aws.String(ec2.InstanceStateNameRunning)},
			},
		},
		{
			{
				Name:   aws.String("private-ip-address"),
				Values: []*string{aws.String(ip)},
			},
			{
				Name:   aws.String("instance-state-name"),
				Values: []*string{aws.String(ec2.InstanceStateNamePending), aws.String(ec2.InstanceStateNameRunning)},
			},
		},
	}

	for _, filters := range lookupFilters {
		result, err := ec2Client.DescribeInstances(&ec2.DescribeInstancesInput{Filters: filters})
		if err != nil {
			return "", fmt.Errorf("failed describing ec2 instances for %s: %w", ip, err)
		}

		for _, reservation := range result.Reservations {
			for _, instance := range reservation.Instances {
				if instance.InstanceId == nil {
					continue
				}

				instanceIDs.Store(ip, aws.StringValue(instance.InstanceId))
				return aws.StringValue(instance.InstanceId), nil
			}
		}
	}

	return "", fmt.Errorf("no ec2 instance found for ip %s", ip)
}

func waitForSSMAgent(instanceID string, maxWait time.Duration) error {
	if err := initAWSClients(); err != nil {
		return err
	}

	if online, ok := ssmOnline.Load(instanceID); ok && online.(bool) {
		return nil
	}

	deadline := time.Now().Add(maxWait)
	attempt := 0

	for time.Now().Before(deadline) {
		attempt++

		result, err := ssmClient.DescribeInstanceInformation(&ssm.DescribeInstanceInformationInput{
			Filters: []*ssm.InstanceInformationStringFilter{
				{
					Key:    aws.String("InstanceIds"),
					Values: []*string{aws.String(instanceID)},
				},
			},
		})
		if err == nil && len(result.InstanceInformationList) > 0 {
			status := aws.StringValue(result.InstanceInformationList[0].PingStatus)
			if status == ssm.PingStatusOnline {
				ssmOnline.Store(instanceID, true)
				return nil
			}
		}

		if attempt == 1 || attempt%10 == 0 {
			log.Printf("Waiting for SSM agent on instance %s", instanceID)
		}

		time.Sleep(3 * time.Second)
	}

	return fmt.Errorf("timed out after %s", maxWait)
}

func runCommandSSM(cmd, instanceID string) (string, error) {
	if err := initAWSClients(); err != nil {
		return "", err
	}

	sendOutput, err := ssmClient.SendCommand(&ssm.SendCommandInput{
		DocumentName: aws.String("AWS-RunShellScript"),
		InstanceIds:  []*string{aws.String(instanceID)},
		Parameters: map[string][]*string{
			"commands": {
				aws.String("set -e"),
				aws.String(cmd),
			},
		},
		TimeoutSeconds: aws.Int64(600),
	})
	if err != nil {
		return "", fmt.Errorf("failed to send ssm command: %w", err)
	}

	commandID := aws.StringValue(sendOutput.Command.CommandId)
	deadline := time.Now().Add(10 * time.Minute)

	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)

		invocation, err := ssmClient.GetCommandInvocation(&ssm.GetCommandInvocationInput{
			CommandId:  aws.String(commandID),
			InstanceId: aws.String(instanceID),
		})
		if err != nil {
			continue
		}

		status := aws.StringValue(invocation.Status)
		switch status {
		case ssm.CommandInvocationStatusSuccess:
			return strings.TrimRight(aws.StringValue(invocation.StandardOutputContent), "\r\n"), nil
		case ssm.CommandInvocationStatusPending, ssm.CommandInvocationStatusInProgress, "Delayed":
			continue
		default:
			stdout := strings.TrimSpace(aws.StringValue(invocation.StandardOutputContent))
			stderr := strings.TrimSpace(aws.StringValue(invocation.StandardErrorContent))
			return "", fmt.Errorf("command status %s\nstdout: %s\nstderr: %s", status, stdout, stderr)
		}
	}

	return "", fmt.Errorf("command %s timed out on instance %s", commandID, instanceID)
}

func (t *Tools) RemoveFile(filePath string) error {
	err := os.Remove(filePath)
	if err != nil {
		return fmt.Errorf("error removing file %s: %w", filePath, err)
	}
	return nil
}

func (t *Tools) RemoveFolder(folderPath string) error {
	err := os.RemoveAll(folderPath)
	if err != nil {
		return fmt.Errorf("error removing folder %s: %w", folderPath, err)
	}
	return nil
}

func (t *Tools) CreateImport(url string, token string, tenantIndex int) error {
	impPay := ImportPayload{
		Type: "provisioning.cattle.io.cluster",
		Metadata: struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		}{
			Namespace: "fleet-default",
			Name:      fmt.Sprintf("imported-tenant-%d", tenantIndex),
		},
		Spec: struct{}{},
	}

	importBody, err := json.Marshal(impPay)
	if err != nil {
		return fmt.Errorf("error marshalling import payload: %v", err)
	}

	client := &http.Client{
		Timeout: time.Second * 10,
	}

	importUrl := fmt.Sprintf("https://%s/v1/provisioning.cattle.io.clusters", url)
	req, err := http.NewRequest("POST", importUrl, bytes.NewBuffer(importBody))
	if err != nil {
		return fmt.Errorf("error setting up http.NewRequest > tools.go > CreateImport: %v", err)
	}

	bearer := fmt.Sprintf("Bearer %s", token)
	req.Header.Set("Authorization", bearer)
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error from client.Do > tools.go > CreateImport: %v", err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Println(err)
		}
	}(response.Body)
	return nil
}

func (t *Tools) GetManifestUrl(url string, token string) string {
	client := &http.Client{
		Timeout: time.Second * 10,
	}

	registrationUrl := fmt.Sprintf("https://%s/v3/clusterregistrationtokens", url)
	req, err := http.NewRequest("GET", registrationUrl, nil)

	if err != nil {
		log.Println(err)
	}

	bearer := fmt.Sprintf("Bearer %s", token)

	req.Header.Set("Authorization", bearer)

	response, err := client.Do(req)
	if err != nil {
		log.Println(err)
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Println(err)
		}
	}(response.Body)

	registrationBytes, err := io.ReadAll(response.Body)
	if err != nil {
		log.Println(err)
	}

	var regResponse RegistrationResponse
	err = json.Unmarshal(registrationBytes, &regResponse)

	if err != nil {
		log.Println(err)
	}

	err = json.Unmarshal(registrationBytes, &regResponse)
	if err != nil {
		log.Println(err)
	}

	// Create a new slice to store the sorted data
	sortedData := make([]struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		ManifestURL string `json:"manifestUrl"`
		CreatedTS   int64  `json:"createdTS"`
	}, len(regResponse.Data))

	// Copy the relevant fields from regResponse.Data to sortedData
	for i, item := range regResponse.Data {
		sortedData[i] = struct {
			ID          string `json:"id"`
			Type        string `json:"type"`
			ManifestURL string `json:"manifestUrl"`
			CreatedTS   int64  `json:"createdTS"`
		}{
			ID:          item.ID,
			Type:        item.Type,
			ManifestURL: item.ManifestURL,
			CreatedTS:   item.CreatedTS,
		}
	}

	// Sort the new slice based on the CreatedTS field in descending order
	sort.Slice(sortedData, func(i, j int) bool {
		return sortedData[i].CreatedTS > sortedData[j].CreatedTS
	})

	if len(sortedData) > 0 {
		return sortedData[0].ManifestURL
	}

	return ""
}

func (t *Tools) CallBashScript(serverUrl, rancherToken string) error {

	cmd := exec.Command("/bin/bash", "./scripts/server-url-update.sh")

	// Set environment variables
	cmd.Env = append(cmd.Env,
		fmt.Sprintf("SERVER_URL=%s", serverUrl),
		fmt.Sprintf("RANCHER_TOKEN=%s", rancherToken),
	)

	out, err := cmd.CombinedOutput()
	if err != nil {
		log.Fatalf("cmd.Run() failed with %s\n", err)
	}

	fmt.Printf("combined out:\n%s\n", string(out))

	return err
}

func (t *Tools) SetupImport(url string, tkn string, tenantIndex int) {

	err := t.CreateImport(url, tkn, tenantIndex)
	if err != nil {
		log.Fatalf("error creating import: %v", err)
	}

	time.Sleep(time.Second * 30)
	manifestUrl := t.GetManifestUrl(url, tkn)
	if manifestUrl == "" {
		log.Fatal("error from tools.go > SetupImport > manifestUrl is empty")
	}

	err = t.GenerateKubectlImportScript(tenantIndex, manifestUrl)
	if err != nil {
		log.Fatalf("error generating import script: %v", err)
	}

	err = os.Setenv("MANIFEST_URL", manifestUrl)
	if err != nil {
		log.Printf("error setting MANIFEST_URL env var: %v", err)
	}
}

func (t *Tools) installK3SCluster(config K3SConfig) string {
	k3sVersion := viper.GetString("k3s.version")

	if err := t.prepareK3SNode(config.Node1IP, config, "SECRET", k3sVersion); err != nil {
		log.Printf("failed preparing first K3s node %s: %v", config.Node1IP, err)
	}

	if err := t.installK3SServer(config.Node1IP, k3sVersion); err != nil {
		log.Printf("failed installing K3s on first node %s: %v", config.Node1IP, err)
	}

	token, err := t.waitForK3SToken(config.Node1IP)
	if err != nil {
		log.Printf("failed waiting for first K3s node token on %s: %v", config.Node1IP, err)
	}

	if err := t.WaitForNodeReady(config.Node1IP); err != nil {
		log.Printf("first K3s node %s is not ready: %v", config.Node1IP, err)
		t.logK3SDiagnostics(config.Node1IP)
	}

	if err := t.prepareK3SNode(config.Node2IP, config, token, k3sVersion); err != nil {
		log.Printf("failed preparing second K3s node %s: %v", config.Node2IP, err)
	}

	if err := t.installK3SServer(config.Node2IP, k3sVersion); err != nil {
		log.Printf("failed installing K3s on second node %s: %v", config.Node2IP, err)
	}

	if err := t.WaitForNodeReady(config.Node2IP); err != nil {
		log.Printf("second K3s node %s is not ready: %v", config.Node2IP, err)
		t.logK3SDiagnostics(config.Node2IP)
	}

	return fmt.Sprintf("https://%s:6443", config.Node1IP)
}

func (t *Tools) prepareK3SNode(nodeIP string, config K3SConfig, token, version string) error {
	if _, err := t.RunCommand("sudo mkdir -p /etc/rancher/k3s /var/lib/rancher/k3s/agent/images", nodeIP); err != nil {
		return fmt.Errorf("failed creating K3s directories: %w", err)
	}

	configContent := buildK3SConfigContent(config, token, nodeIP)
	if err := t.writeRemoteFile(nodeIP, "/etc/rancher/k3s/config.yaml", configContent); err != nil {
		return fmt.Errorf("failed writing K3s config: %w", err)
	}

	dockerHubUser, dockerHubPassword := dockerHubCredentials()
	if dockerHubUser != "" && dockerHubPassword != "" {
		registriesContent := buildK3SRegistriesContent(dockerHubUser, dockerHubPassword)
		if err := t.writeRemoteFile(nodeIP, "/etc/rancher/k3s/registries.yaml", registriesContent); err != nil {
			return fmt.Errorf("failed writing registries config: %w", err)
		}
	}

	if !viper.GetBool("k3s.preload_images") {
		return nil
	}

	airgapURL := buildK3SAirgapImageURL(version)
	airgapSHA256, err := k3SChecksumForVersion("k3s.airgap_image_sha256s", version)
	if err != nil {
		return err
	}

	cmd := fmt.Sprintf(
		`tmp_images="$(mktemp /tmp/k3s-airgap-images-amd64.XXXXXX)"
trap 'rm -f "$tmp_images"' EXIT

curl -fsSL -o "$tmp_images" %s

if ! echo %s"  $tmp_images" | sha256sum -c -; then
  echo "############################################################" >&2
  echo "# SECURITY ERROR: K3s image checksum validation failed      #" >&2
  echo "# Refusing to preload the downloaded image bundle.          #" >&2
  echo "# Check k3s.version and k3s.airgap_image_sha256s.           #" >&2
  echo "############################################################" >&2
  exit 1
fi

sudo mv "$tmp_images" /var/lib/rancher/k3s/agent/images/k3s-airgap-images-amd64.tar.zst
trap - EXIT`,
		shellQuote(airgapURL),
		shellQuote(airgapSHA256),
	)
	if _, err := t.RunCommand(cmd, nodeIP); err != nil {
		return fmt.Errorf("failed preloading K3s images from %s: %w", airgapURL, err)
	}

	return nil
}

func (t *Tools) installK3SServer(nodeIP, version string) error {
	installScriptSHA256, err := k3SChecksumForVersion("k3s.install_script_sha256s", version)
	if err != nil {
		return err
	}

	installScriptURL := buildK3SInstallScriptURL(version)
	cmd := fmt.Sprintf(
		`tmp_script="$(mktemp /tmp/k3s-install.XXXXXX)"
trap 'rm -f "$tmp_script"' EXIT

curl -fsSL -o "$tmp_script" %s

if ! echo %s"  $tmp_script" | sha256sum -c -; then
  echo "############################################################" >&2
  echo "# SECURITY ERROR: K3s installer checksum validation failed  #" >&2
  echo "# Refusing to run the downloaded installer.                 #" >&2
  echo "# Check k3s.version and k3s.install_script_sha256s.         #" >&2
  echo "############################################################" >&2
  exit 1
fi

sudo INSTALL_K3S_VERSION=%s sh "$tmp_script" server`,
		shellQuote(installScriptURL),
		shellQuote(installScriptSHA256),
		shellQuote(version),
	)
	if _, err := t.RunCommand(cmd, nodeIP); err != nil {
		t.logK3SDiagnostics(nodeIP)
		return err
	}

	return nil
}

func (t *Tools) waitForK3SToken(nodeIP string) (string, error) {
	timeout := time.After(5 * time.Minute)
	poll := time.Tick(10 * time.Second)

	for {
		select {
		case <-timeout:
			t.logK3SDiagnostics(nodeIP)
			return "", fmt.Errorf("timed out waiting for K3s token on %s", nodeIP)
		case <-poll:
			token, err := t.RunCommand("sudo test -s /var/lib/rancher/k3s/server/token && sudo cat /var/lib/rancher/k3s/server/token", nodeIP)
			if err != nil {
				continue
			}
			token = strings.TrimSpace(token)
			if token != "" {
				return token, nil
			}
		}
	}
}

func (t *Tools) writeRemoteFile(nodeIP, path, content string) error {
	cmd := fmt.Sprintf("cat <<'EOF' | sudo tee %s >/dev/null\n%s\nEOF", shellQuote(path), content)
	_, err := t.RunCommand(cmd, nodeIP)
	return err
}

func (t *Tools) logK3SDiagnostics(nodeIP string) {
	commands := []string{
		"sudo systemctl status k3s --no-pager || true",
		"sudo journalctl -u k3s --no-pager -n 50 || true",
	}

	for _, cmd := range commands {
		output, err := t.RunCommand(cmd, nodeIP)
		if err != nil {
			log.Printf("failed collecting diagnostics on %s with %q: %v", nodeIP, cmd, err)
			continue
		}
		log.Printf("K3s diagnostics from %s:\n%s", nodeIP, output)
	}
}

func buildK3SConfigContent(config K3SConfig, token, nodeIP string) string {
	tlsSANs := []string{
		config.RancherURL,
		config.Node1IP,
		config.Node2IP,
	}

	lines := []string{
		fmt.Sprintf("token: %s", yamlQuote(token)),
		fmt.Sprintf("datastore-endpoint: %s", yamlQuote(fmt.Sprintf("mysql://tfadmin:%s@tcp(%s)/k3s", config.DBPassword, config.DBEndpoint))),
		"tls-san:",
	}

	for _, san := range tlsSANs {
		if san == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("  - %s", yamlQuote(san)))
	}

	lines = append(lines, fmt.Sprintf("node-external-ip: %s", yamlQuote(nodeIP)))

	return strings.Join(lines, "\n")
}

func buildK3SRegistriesContent(username, password string) string {
	return strings.Join([]string{
		"configs:",
		"  docker.io:",
		"    auth:",
		fmt.Sprintf("      username: %s", yamlQuote(username)),
		fmt.Sprintf("      password: %s", yamlQuote(password)),
	}, "\n")
}

func buildK3SAirgapImageURL(version string) string {
	escapedVersion := strings.ReplaceAll(version, "+", "%2B")
	return fmt.Sprintf("https://github.com/k3s-io/k3s/releases/download/%s/k3s-airgap-images-amd64.tar.zst", escapedVersion)
}

func buildK3SInstallScriptURL(version string) string {
	escapedVersion := strings.ReplaceAll(version, "+", "%2B")
	return fmt.Sprintf("https://raw.githubusercontent.com/k3s-io/k3s/%s/install.sh", escapedVersion)
}

func k3SChecksumForVersion(configKey, version string) (string, error) {
	checksums := viper.GetStringMapString(configKey)
	checksum := strings.TrimSpace(checksums[version])
	if checksum == "" {
		return "", fmt.Errorf("%s.%s must be set", configKey, version)
	}

	return checksum, nil
}

func dockerHubCredentials() (string, string) {
	username := strings.TrimSpace(os.Getenv("DOCKERHUB_USERNAME"))
	password := strings.TrimSpace(os.Getenv("DOCKERHUB_PASSWORD"))
	if username != "" || password != "" {
		return username, password
	}

	return strings.TrimSpace(viper.GetString("dockerhub.username")), strings.TrimSpace(viper.GetString("dockerhub.password"))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func yamlQuote(value string) string {
	quoted, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%q", value)
	}

	return string(quoted)
}

func (t *Tools) GenerateKubectlImportScript(tenantIndex int, manifestUrl string) error {
	scriptContent := fmt.Sprintf(`#!/bin/bash
set -e

echo "Importing tenant cluster into host Rancher..."
echo "Manifest URL: %s"

# Apply the import manifest
export KUBECONFIG=kube_config.yaml
kubectl apply -f "%s" --validate=false --insecure-skip-tls-verify

echo "Import manifest applied successfully!"
`, manifestUrl, manifestUrl)

	// Get current working directory and create absolute path
	currentDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	scriptDir := fmt.Sprintf("tenant-%d-rancher", tenantIndex)
	absScriptDir := filepath.Join(currentDir, scriptDir)

	// Make sure the directory exists
	err = os.MkdirAll(absScriptDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create script directory: %w", err)
	}

	scriptPath := filepath.Join(absScriptDir, "import.sh")

	err = os.WriteFile(scriptPath, []byte(scriptContent), 0755)
	if err != nil {
		return fmt.Errorf("failed to write import script for tenant %d: %v", tenantIndex, err)
	}

	log.Printf("Created import script: %s", scriptPath)
	return nil
}

type LoginPayload struct {
	Description  string `json:"description"`
	ResponseType string `json:"responseType"`
	Username     string `json:"username"`
	Password     string `json:"password"`
}

type LoginResponse struct {
	Token string `json:"token"`
}

type TokenBody struct {
	Type        string      `json:"type"`
	Metadata    interface{} `json:"metadata"`
	Description string      `json:"description"`
	TTL         int         `json:"ttl"`
}

type TokenResponse struct {
	Token string `json:"token"`
}

type ImportPayload struct {
	Type     string `json:"type"`
	Metadata struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"metadata"`
	Spec struct{} `json:"spec"`
}

type RegistrationResponse struct {
	Data []struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		ManifestURL string `json:"manifestUrl"`
		CreatedTS   int64  `json:"createdTS"`
	} `json:"data"`
}
