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
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/spf13/viper"
)

const (
	randomStringSource = "abcdefghijklmnopqrstuvwxyz"
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
			// Check the K3S service status.
			nodeStatus, err := t.RunCommand("systemctl is-active k3s", nodeIP)
			if err != nil {
				return fmt.Errorf("failed to check node status: %w", err)
			}

			// If the K3S service is running (i.e., the status is "active"), return nil.
			if strings.TrimSpace(nodeStatus) == "active" {
				return nil
			}
		}
	}
}

func (t *Tools) K3SHostInstall(config K3SConfig) string {

	k3sVersion := viper.GetString("k3s.version")

	nodeOneCommand := nodeCommandBuilder(k3sVersion, "SECRET", config.DBPassword, config.DBEndpoint, config.RancherURL, config.Node1IP)

	_, err := t.RunCommand(nodeOneCommand, config.Node1IP)
	if err != nil {
		log.Println(err)
	}

	token, err := t.RunCommand("sudo cat /var/lib/rancher/k3s/server/token", config.Node1IP)
	if err != nil {
		log.Println(err)
	}

	// Wait for node one to be ready
	err = t.WaitForNodeReady(config.Node1IP)
	if err != nil {
		log.Println("node one is not ready: %w", err)
	}

	nodeTwoCommand := nodeCommandBuilder(k3sVersion, token, config.DBPassword, config.DBEndpoint, config.RancherURL, config.Node2IP)
	_, err = t.RunCommand(nodeTwoCommand, config.Node2IP)
	if err != nil {
		log.Println(err)
	}

	// Wait for node two to be ready
	err = t.WaitForNodeReady(config.Node2IP)
	if err != nil {
		log.Println("node two is not ready: %w", err)
	}

	configIP := fmt.Sprintf("https://%s:6443", config.Node1IP)
	return configIP
}

func (t *Tools) K3STenantInstall(config K3SConfig) string {
	k3sVersion := viper.GetString("k3s.version")

	nodeOneCommand := nodeCommandBuilder(k3sVersion, "SECRET", config.DBPassword, config.DBEndpoint, config.RancherURL, config.Node1IP)

	_, err := t.RunCommand(nodeOneCommand, config.Node1IP)
	if err != nil {
		log.Println(err)
	}

	token, err := t.RunCommand("sudo cat /var/lib/rancher/k3s/server/token", config.Node1IP)
	if err != nil {
		log.Println(err)
	}

	// Wait for node one to be ready
	err = t.WaitForNodeReady(config.Node1IP)
	if err != nil {
		log.Println("node one is not ready: %w", err)
	}

	nodeTwoCommand := nodeCommandBuilder(k3sVersion, token, config.DBPassword, config.DBEndpoint, config.RancherURL, config.Node2IP)
	_, err = t.RunCommand(nodeTwoCommand, config.Node2IP)
	if err != nil {
		log.Println(err)
	}

	// Wait for node two to be ready
	err = t.WaitForNodeReady(config.Node2IP)
	if err != nil {
		log.Println("node two is not ready: %w", err)
	}

	configIP := fmt.Sprintf("https://%s:6443", config.Node1IP)

	return configIP
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

	pemKey := viper.GetString("aws.rsa_private_key")

	dialIP := fmt.Sprintf("%s:22", pubIP)

	signer, err := ssh.ParsePrivateKey([]byte(pemKey))
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}
	config := &ssh.ClientConfig{
		User:            "ubuntu",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	conn, err := ssh.Dial("tcp", dialIP, config)
	if err != nil {
		return "", fmt.Errorf("failed to establish ssh connection: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			log.Println(err)
		}
	}()

	session, err := conn.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create new ssh session: %w", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			log.Println(err)
		}
	}()

	var stdoutBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	err = session.Run(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to run ssh command: %w", err)
	}

	stringOut := stdoutBuf.String()
	stringOut = strings.TrimRight(stringOut, "\r\n")

	return stringOut, nil
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

func nodeCommandBuilder(version, secret, password, endpoint, url, ip string) string {
	return fmt.Sprintf(`curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION='%s' sh -s - server --token=%s --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, version, secret, password, endpoint, url, ip)
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
