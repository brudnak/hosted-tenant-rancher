package toolkit

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io"
	"io/ioutil"
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

func (t *Tools) SetupK3S(mysqlPassword string, mysqlEndpoint string, rancherURL string, node1IP string, node2IP string, rancherType string) int {

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
		err = os.WriteFile("../../kube/config/host.yml", output, 0644)
		if err != nil {
			log.Println("failed creating host config:", err)
		}
	} else if rancherType == "tenant" {
		err = os.WriteFile("../../kube/config/tenant.yml", output, 0644)
		if err != nil {
			log.Println("failed creating tenant config:", err)
		}
	} else {
		log.Fatal("expecting either host or tenant for rancher type")
	}

	tfvarFile := fmt.Sprintf("rancher_url = \"%s\"\nbootstrap_password = \"%s\"\nemail = \"%s\"\nrancher_version = \"%s\"", rancherURL, viper.GetString("rancher.bootstrap_password"), viper.GetString("rancher.email"), viper.GetString("rancher.version"))
	tfvarFileBytes := []byte(tfvarFile)

	if rancherType == "host" {
		err = os.WriteFile("../modules/helm/host/terraform.tfvars", tfvarFileBytes, 0644)

		if err != nil {
			log.Println("failed creating host tfvars:", err)
		}
	} else if rancherType == "tenant" {
		err = os.WriteFile("../modules/helm/tenant/terraform.tfvars", tfvarFileBytes, 0644)

		if err != nil {
			log.Println("failed creating tenant tfvars:", err)
		}
	} else {
		log.Fatal("expecting either host or tenant for rancher type")
	}

	return actualNodeCount
}

func (t *Tools) CreateToken(password string, url string) string {

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

	adminLogin := fmt.Sprintf("%s/v3-public/localProviders/local?action=login", url)
	resp, err := http.Post(adminLogin, "application/jsona", bytes.NewBuffer(loginBody))

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Println(err)
	}

	var loginResp LoginResponse
	json.Unmarshal(b, &loginResp)

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

	tokenUrl := fmt.Sprintf("%s/v3/tokens", url)
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
	defer response.Body.Close()

	tokenBytes, err := ioutil.ReadAll(response.Body)
	if err != nil {
		log.Println(err)
	}

	var tokenRes TokenResponse
	json.Unmarshal(tokenBytes, &tokenRes)

	return tokenRes.Token
}

func (t *Tools) RunCommand(cmd string, pubIP string) string {

	path := viper.GetString("local.pem_path")

	dialIP := fmt.Sprintf("%s:22", pubIP)

	pemBytes, err := ioutil.ReadFile(path)
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
	defer conn.Close()
	session, err := conn.NewSession()
	if err != nil {
		log.Fatalf("session failed:%v", err)
	}
	defer session.Close()
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
