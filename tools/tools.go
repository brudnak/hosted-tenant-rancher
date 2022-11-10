package toolkit

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/brudnak/hosted-tenant-rancher/terratest/util"
	"github.com/spf13/viper"
)

const randomStringSource = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ!@#$%^&*"

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

	viper.AddConfigPath("../config")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	viper.ReadInConfig()

	k3sVersion := viper.GetString("k3s.version")

	nodeOneCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION='%s' sh -s - server --token=SECRET --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, k3sVersion, mysqlPassword, mysqlEndpoint, rancherURL, node1IP)

	var _ = util.RunCommand(nodeOneCommand, node1IP)

	token := util.RunCommand("sudo cat /var/lib/rancher/k3s/server/token", node1IP)
	serverKubeConfig := util.RunCommand("sudo cat /etc/rancher/k3s/k3s.yaml", node1IP)

	time.Sleep(10 * time.Second)

	nodeTwoCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION='%s' sh -s - server --token=%s --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, k3sVersion, token, mysqlPassword, mysqlEndpoint, rancherURL, node2IP)
	var _ = util.RunCommand(nodeTwoCommand, node2IP)

	time.Sleep(10 * time.Second)

	wcResponse := util.RunCommand("sudo k3s kubectl get nodes | wc -l", node1IP)
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
