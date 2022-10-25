package test

import (
	"bytes"
	"fmt"
	"golang.org/x/crypto/ssh"
	"io/ioutil"
	"log"
	"net"
	"strings"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/assert"
)

func TestHostInfrastructureCreate(t *testing.T) {
	t.Parallel()

	var dataStoreEndpoint string
	var dataStorePassword string
	var publicIP string
	var publicIP2 string
	var rancherURL string

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../basetf/hosted",
		NoColor:      true,
	})

	// defer terraform.Destroy(t, terraformOptions)

	terraform.InitAndApply(t, terraformOptions)
	publicIP = terraform.Output(t, terraformOptions, "server_ip")
	publicIP2 = terraform.Output(t, terraformOptions, "server_ip2")
	dataStoreEndpoint = terraform.Output(t, terraformOptions, "db_endpoint")
	dataStorePassword = terraform.Output(t, terraformOptions, "db_password")
	rancherURL = terraform.Output(t, terraformOptions, "rancher_url")

	checkedIP1 := checkIPAddress(publicIP)

	assert.Equal(t, "valid", checkedIP1)

	firstServerCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | sh -s - server --token=SECRET --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, dataStorePassword, dataStoreEndpoint, rancherURL, publicIP)

	firstServerInstallOutput := runCommand(firstServerCommand, publicIP)
	log.Println(firstServerInstallOutput)

	token := runCommand("sudo cat /var/lib/rancher/k3s/server/token", publicIP)
	token = strings.TrimRight(token, "\r\n")
	log.Println("tokenCmd:", token)

	firstServerKubeconfig := runCommand("sudo cat /etc/rancher/k3s/k3s.yaml", publicIP)
	log.Println(firstServerKubeconfig)

	secondServerCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | sh -s - server --token=%s --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, token, dataStorePassword, dataStoreEndpoint, rancherURL, publicIP2)
	fmt.Println("look here pub ip 2:", publicIP2)
	fmt.Println("look here 2nd cmd:", secondServerCommand)
	secondServerInstallOutput := runCommand(secondServerCommand, publicIP2)
	log.Println(secondServerInstallOutput)
}

func TestHostCleanup(t *testing.T) {
	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../basetf/hosted",
		NoColor:      true,
	})
	terraform.Destroy(t, terraformOptions)
}

func checkIPAddress(ip string) string {
	if net.ParseIP(ip) == nil {
		return "invalid"
	} else {
		return "valid"
	}
}

func runCommand(cmd string, pubIP string) string {

	dialIP := fmt.Sprintf("%s:22", pubIP)

	pemBytes, err := ioutil.ReadFile("")
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

	return stringOut
}
