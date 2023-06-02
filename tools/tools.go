package toolkit

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/brudnak/hosted-tenant-rancher/tools/hcl"
	"github.com/go-rod/rod"
	"golang.org/x/crypto/ssh"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

const randomStringSource = "abcdefghijklmnopqrstuvwxyz"

type Tools struct{}

func (t *Tools) RandomString(n int) string {
	s, r := make([]rune, n), []rune(randomStringSource)
	for i := range s {
		p, _ := rand.Prime(rand.Reader, len(r))
		x, y := p.Uint64(), uint64(len(r))
		s[i] = r[x%y]
	}
	return string(s)
}

func (t *Tools) SetupK3S(mysqlPassword string, mysqlEndpoint string, rancherURL string, node1IP string, node2IP string, rancherType string) (int, string) {

	k3sVersion := viper.GetString("k3s.version")

	nodeOneCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION='%s' sh -s - server --token=SECRET --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, k3sVersion, mysqlPassword, mysqlEndpoint, rancherURL, node1IP)

	var _ = t.RunCommand(nodeOneCommand, node1IP)

	token := t.RunCommand("sudo cat /var/lib/rancher/k3s/server/token", node1IP)
	serverKubeConfig := t.RunCommand("sudo cat /etc/rancher/k3s/k3s.yaml", node1IP)

	time.Sleep(10 * time.Second)

	nodeTwoCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION='%s' sh -s - server --token=%s --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, k3sVersion, token, mysqlPassword, mysqlEndpoint, rancherURL, node2IP)
	var _ = t.RunCommand(nodeTwoCommand, node2IP)

	time.Sleep(10 * time.Second)

	wcResponse := t.RunCommand("sudo k3s kubectl get nodes | wc -l", node1IP)
	actualNodeCount, err := strconv.Atoi(wcResponse)
	actualNodeCount = actualNodeCount - 1

	if err != nil {
		log.Println(err)
	}

	kubeConf := []byte(serverKubeConfig)

	configIP := fmt.Sprintf("https://%s:6443", node1IP)
	output := bytes.Replace(kubeConf, []byte("https://127.0.0.1:6443"), []byte(configIP), -1)

	if rancherType == "host" {
		err = os.WriteFile("../../host.yml", output, 0644)
		if err != nil {
			log.Println("failed creating host config:", err)
		}
	} else if rancherType == "tenant" {
		err = os.WriteFile("../../tenant.yml", output, 0644)
		if err != nil {
			log.Println("failed creating tenant config:", err)
		}
		err = os.WriteFile("../../terratest/modules/kubectl/theconfig.yml", output, 0644)
		if err != nil {
			log.Println("failed creating tenant config:", err)
		}
	} else {
		log.Fatal("expecting either host or tenant for rancher type")
	}

	pspVal := viper.GetBool("rancher.psp_bool")

	var tfvarFile string

	if pspVal == false {
		tfvarFile = fmt.Sprintf("rancher_url = \"%s\"\nbootstrap_password = \"%s\"\nemail = \"%s\"\nrancher_version = \"%s\"\nimage_tag = \"%s\"\npsp_bool = \"%v\"", rancherURL, viper.GetString("rancher.bootstrap_password"), viper.GetString("rancher.email"), viper.GetString("rancher.version"), viper.GetString("rancher.image_tag"), pspVal)
	} else {
		tfvarFile = fmt.Sprintf("rancher_url = \"%s\"\nbootstrap_password = \"%s\"\nemail = \"%s\"\nrancher_version = \"%s\"\nimage_tag = \"%s\"", rancherURL, viper.GetString("rancher.bootstrap_password"), viper.GetString("rancher.email"), viper.GetString("rancher.version"), viper.GetString("rancher.image_tag"))
	}

	tfvarFileBytes := []byte(tfvarFile)

	if rancherType == "host" {
		err = os.WriteFile("../modules/helm/host/terraform.tfvars", tfvarFileBytes, 0644)
		hcl.GenHelmVar(
			rancherURL,
			viper.GetString("rancher.bootstrap_password"),
			viper.GetString("rancher.email"),
			viper.GetString("upgrade.version"),
			viper.GetString("upgrade.image_tag"),
			"../modules/helm/host/upgrade.tfvars",
			viper.GetBool("rancher.psp_bool"))

		if err != nil {
			log.Println("failed creating host tfvars:", err)
		}
	} else if rancherType == "tenant" {
		err = os.WriteFile("../modules/helm/tenant/terraform.tfvars", tfvarFileBytes, 0644)
		hcl.GenHelmVar(
			rancherURL,
			viper.GetString("rancher.bootstrap_password"),
			viper.GetString("rancher.email"),
			viper.GetString("upgrade.version"),
			viper.GetString("upgrade.image_tag"),
			"../modules/helm/tenant/upgrade.tfvars",
			viper.GetBool("rancher.psp_bool"))
		if err != nil {
			log.Println("failed creating tenant tfvars:", err)
		}
	} else {
		log.Fatal("expecting either host or tenant for rancher type")
	}

	return actualNodeCount, configIP
}

func (t *Tools) CreateToken(url string, password string) string {

	loginPayload := LoginPayload{
		Description:  t.RandomString(6),
		ResponseType: "token",
		Username:     "admin",
		Password:     password,
	}

	loginBody, err := json.Marshal(loginPayload)

	if err != nil {
		log.Println(err)
	}

	adminLogin := fmt.Sprintf("https://%s/v3-public/localProviders/local?action=login", url)
	resp, err := http.Post(adminLogin, "application/json", bytes.NewBuffer(loginBody))

	if resp == nil {
		log.Println("response was nil")
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println(err)
	}

	var loginResp LoginResponse
	err = json.Unmarshal(b, &loginResp)

	if err != nil {
		log.Println(err)
	}

	client := &http.Client{
		Timeout: time.Second * 10,
	}

	tokenBody := TokenBody{
		Type:        "token",
		Metadata:    struct{}{},
		Description: "ADMIN_TOKEN",
		TTL:         0,
	}

	jsonTokenBody, err := json.Marshal(tokenBody)
	if err != nil {
		log.Println(err)
	}

	tokenUrl := fmt.Sprintf("https://%s/v3/tokens", url)
	req, err := http.NewRequest("POST", tokenUrl, bytes.NewBuffer(jsonTokenBody))

	if err != nil {
		log.Println(err)
	}

	bearer := fmt.Sprintf("Bearer %s", loginResp.Token)

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

	tokenBytes, err := io.ReadAll(response.Body)
	if err != nil {
		log.Println(err)
	}

	var tokenRes TokenResponse
	err = json.Unmarshal(tokenBytes, &tokenRes)

	if err != nil {
		log.Println(err)
	}

	return tokenRes.Token
}

func (t *Tools) RunCommand(cmd string, pubIP string) string {

	path := viper.GetString("local.pem_path")

	dialIP := fmt.Sprintf("%s:22", pubIP)

	pemBytes, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}

	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		log.Fatalf("parse key failed:%v", err)
	}
	config := &ssh.ClientConfig{
		User:            "ubuntu",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}
	conn, err := ssh.Dial("tcp", dialIP, config)
	if err != nil {
		log.Fatalf("dial failed:%v", err)
	}
	defer func(conn *ssh.Client) {
		err := conn.Close()
		if err != nil {
			log.Println(err)
		}
	}(conn)
	session, err := conn.NewSession()
	if err != nil {
		log.Fatalf("session failed:%v", err)
	}
	defer func(session *ssh.Session) {
		err := session.Close()
		if err != nil {
			log.Println(err)
		}
	}(session)
	var stdoutBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	err = session.Run(cmd)
	if err != nil {
		log.Fatalf("Run failed:%v", err)
	}

	stringOut := stdoutBuf.String()

	stringOut = strings.TrimRight(stringOut, "\r\n")

	return stringOut
}

func (t *Tools) CheckIPAddress(ip string) string {
	if net.ParseIP(ip) == nil {
		return "invalid"
	} else {
		return "valid"
	}
}

func (t *Tools) RemoveFile(filePath string) {
	err := os.Remove(filePath)
	if err != nil {
		log.Println(err)
	}
}

func (t *Tools) RemoveFolder(folderPath string) {
	err := os.RemoveAll(folderPath)
	if err != nil {
		log.Println(err)
	}
}

func (t *Tools) CreateImport(url string, token string) {

	impPay := ImportPayload{
		Type: "provisioning.cattle.io.cluster",
		Metadata: struct {
			Namespace string `json:"namespace"`
			Name      string `json:"name"`
		}{
			Namespace: "fleet-default",
			Name:      "imported-tenant",
		},
		Spec: struct{}{},
	}

	importBody, err := json.Marshal(impPay)
	if err != nil {
		log.Println(err)
	}

	client := &http.Client{
		Timeout: time.Second * 10,
	}

	importUrl := fmt.Sprintf("https://%s/v1/provisioning.cattle.io.clusters", url)
	req, err := http.NewRequest("POST", importUrl, bytes.NewBuffer(importBody))

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

	log.Println("LOOK HERE:", regResponse.Data[0].ManifestURL)

	return regResponse.Data[0].ManifestURL
}

func (t *Tools) WorkAround(url, password string) {

	loginUrl := fmt.Sprintf("https://%s/dashboard/auth/login", url)

	browser := rod.New().MustConnect().NoDefaultDevice()
	page := browser.MustPage(loginUrl).MustWindowFullscreen()

	page.MustElement("#password > div > input[type=password]").MustInput(password)
	page.MustElement("#submit").MustClick()
	time.Sleep(5 * time.Second)

	page.MustElement("#__layout > main > div > form > div > div.col.span-6.form-col > div:nth-child(2) > div.checkbox.mt-40 > div > label > span.checkbox-label").MustClick()
	page.MustElement("#__layout > main > div > form > div > div.col.span-6.form-col > div:nth-child(2) > div.checkbox.pt-10.eula > div > label > span.checkbox-custom").MustClick()

	time.Sleep(5 * time.Second)
	page.MustElement("#submit > button").MustClick()
}

func (t *Tools) SetupImport(url string, password string, ip string) {

	adminToken := t.CreateToken(url, password)
	t.CreateImport(url, adminToken)
	time.Sleep(time.Minute * 2)
	manifestUrl := t.GetManifestUrl(url, adminToken)
	hcl.GenerateKubectlTfVar(ip, manifestUrl)
	err := os.Setenv("KUBECONFIG", "theconfig.yml")

	if err != nil {
		fmt.Println("error setup import:", err)
		return
	}
}
