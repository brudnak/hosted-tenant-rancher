package test

import (
	"bytes"
	"fmt"
	"github.com/brudnak/hosted-tenant-rancher/terratest/util"
	"github.com/spf13/viper"
	"os"
	"strconv"
	"time"

	"log"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/assert"
)

func TestHostInfrastructureCreate(t *testing.T) {

	var mysqlEndpoint string
	var mysqlPassword string
	var nodeOneIPAddress string
	var nodeTwoIPAddress string
	var rancherURL string

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../basetf/hosted",
		NoColor:      true,
	})

	terraform.InitAndApply(t, terraformOptions)

	nodeOneIPAddress = terraform.Output(t, terraformOptions, "server_ip")
	nodeTwoIPAddress = terraform.Output(t, terraformOptions, "server_ip2")
	mysqlEndpoint = terraform.Output(t, terraformOptions, "db_endpoint")
	mysqlPassword = terraform.Output(t, terraformOptions, "db_password")
	rancherURL = terraform.Output(t, terraformOptions, "rancher_url")

	noneOneIPAddressValidationResult := util.CheckIPAddress(nodeOneIPAddress)
	nodeTwoIPAddressValidationResult := util.CheckIPAddress(nodeTwoIPAddress)

	assert.Equal(t, "valid", noneOneIPAddressValidationResult)
	assert.Equal(t, "valid", nodeTwoIPAddressValidationResult)

	nodeOneCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | sh -s - server --token=SECRET --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, mysqlPassword, mysqlEndpoint, rancherURL, nodeOneIPAddress)

	var _ = util.RunCommand(nodeOneCommand, nodeOneIPAddress)

	token := util.RunCommand("sudo cat /var/lib/rancher/k3s/server/token", nodeOneIPAddress)
	serverKubeConfig := util.RunCommand("sudo cat /etc/rancher/k3s/k3s.yaml", nodeOneIPAddress)

	time.Sleep(60 * time.Second)

	nodeTwoCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | sh -s - server --token=%s --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, token, mysqlPassword, mysqlEndpoint, rancherURL, nodeTwoIPAddress)
	var _ = util.RunCommand(nodeTwoCommand, nodeTwoIPAddress)

	time.Sleep(60 * time.Second)

	wcResponse := util.RunCommand("sudo k3s kubectl get nodes | wc -l", nodeOneIPAddress)
	actualNodeCount, err := strconv.Atoi(wcResponse)
	actualNodeCount = actualNodeCount - 1

	if err != nil {
		log.Println(err)
	}

	assert.Equal(t, 2, actualNodeCount)

	kubeConf := []byte(serverKubeConfig)

	configIP := fmt.Sprintf("https://%s:6443", nodeOneIPAddress)
	output := bytes.Replace(kubeConf, []byte("https://127.0.0.1:6443"), []byte(configIP), -1)

	err = os.WriteFile("../config/kube_config.yml", output, 0644)
	if err != nil {
		panic(err)
	}

	viper.AddConfigPath("../config")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	viper.ReadInConfig()

	tfvarFile := fmt.Sprintf("rancher_url = \"%s\"\nbootstrap_password = \"%s\"\nemail = \"%s\"\nrancher_version = \"%s\"", rancherURL, viper.GetString("rancher.bootstrap_password"), viper.GetString("rancher.email"), viper.GetString("rancher.version"))
	tfvarFileBytes := []byte(tfvarFile)

	err = os.WriteFile("../basetf/helm-host/terraform.tfvars", tfvarFileBytes, 0644)

	if err != nil {
		log.Println(err)
	}

	t.Run("install rancher", TestInstallHostRancher)
	log.Println("Rancher url", rancherURL)
}

func TestInstallHostRancher(t *testing.T) {

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../basetf/helm-host",
		NoColor:      true,
	})

	terraform.InitAndApply(t, terraformOptions)
}

func TestHostCleanup(t *testing.T) {
	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: "../basetf/hosted",
		NoColor:      true,
	})

	err := os.Remove("../config/kube_config.yml")
	if err != nil {
		log.Println(err)
	}

	err = os.Remove("../basetf/helm-host/terraform.tfvars")
	if err != nil {
		log.Println(err)
	}

	terraform.Destroy(t, terraformOptions)
}
