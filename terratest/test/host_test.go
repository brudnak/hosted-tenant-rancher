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

	var infra1MysqlEndpoint string
	var infra1MysqlPassword string
	var infra1Server1IPAddress string
	var infra1Server2IPAddress string
	var infra1RancherURL string

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../modules/aws",
		NoColor:      true,
	})

	terraform.InitAndApply(t, terraformOptions)

	infra1Server1IPAddress = terraform.Output(t, terraformOptions, "infra1_server1_ip")
	infra1Server2IPAddress = terraform.Output(t, terraformOptions, "infra1_server2_ip")
	infra1MysqlEndpoint = terraform.Output(t, terraformOptions, "infra1_mysql_endpoint")
	infra1MysqlPassword = terraform.Output(t, terraformOptions, "infra1_mysql_password")
	infra1RancherURL = terraform.Output(t, terraformOptions, "infra1_rancher_url")

	noneOneIPAddressValidationResult := util.CheckIPAddress(infra1Server1IPAddress)
	nodeTwoIPAddressValidationResult := util.CheckIPAddress(infra1Server2IPAddress)

	assert.Equal(t, "valid", noneOneIPAddressValidationResult)
	assert.Equal(t, "valid", nodeTwoIPAddressValidationResult)

	nodeOneCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | sh -s - server --token=SECRET --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, infra1MysqlPassword, infra1MysqlEndpoint, infra1RancherURL, infra1Server1IPAddress)

	var _ = util.RunCommand(nodeOneCommand, infra1Server1IPAddress)

	token := util.RunCommand("sudo cat /var/lib/rancher/k3s/server/token", infra1Server1IPAddress)
	serverKubeConfig := util.RunCommand("sudo cat /etc/rancher/k3s/k3s.yaml", infra1Server1IPAddress)

	time.Sleep(60 * time.Second)

	nodeTwoCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | sh -s - server --token=%s --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, token, infra1MysqlPassword, infra1MysqlEndpoint, infra1RancherURL, infra1Server2IPAddress)
	var _ = util.RunCommand(nodeTwoCommand, infra1Server2IPAddress)

	time.Sleep(60 * time.Second)

	wcResponse := util.RunCommand("sudo k3s kubectl get nodes | wc -l", infra1Server1IPAddress)
	actualNodeCount, err := strconv.Atoi(wcResponse)
	actualNodeCount = actualNodeCount - 1

	if err != nil {
		log.Println(err)
	}

	assert.Equal(t, 2, actualNodeCount)

	kubeConf := []byte(serverKubeConfig)

	configIP := fmt.Sprintf("https://%s:6443", infra1Server1IPAddress)
	output := bytes.Replace(kubeConf, []byte("https://127.0.0.1:6443"), []byte(configIP), -1)

	err = os.WriteFile("../config/kube_config.yml", output, 0644)
	if err != nil {
		panic(err)
	}

	viper.AddConfigPath("../config")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	viper.ReadInConfig()

	tfvarFile := fmt.Sprintf("rancher_url = \"%s\"\nbootstrap_password = \"%s\"\nemail = \"%s\"\nrancher_version = \"%s\"", infra1RancherURL, viper.GetString("rancher.bootstrap_password"), viper.GetString("rancher.email"), viper.GetString("rancher.version"))
	tfvarFileBytes := []byte(tfvarFile)

	err = os.WriteFile("../modules/helm/terraform.tfvars", tfvarFileBytes, 0644)

	if err != nil {
		log.Println(err)
	}

	t.Run("install rancher", TestInstallHostRancher)
	log.Println("Rancher url", infra1RancherURL)
}

func TestInstallHostRancher(t *testing.T) {

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../modules/helm",
		NoColor:      true,
	})

	terraform.InitAndApply(t, terraformOptions)
}

func TestHostCleanup(t *testing.T) {
	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: "../modules/aws",
		NoColor:      true,
	})

	err := os.Remove("../config/kube_config.yml")
	if err != nil {
		log.Println(err)
	}

	err = os.Remove("../modules/helm/terraform.tfvars")
	if err != nil {
		log.Println(err)
	}

	terraform.Destroy(t, terraformOptions)
}
