package test

import (
	"bytes"
	"fmt"
	"github.com/spf13/viper"
	"os"
	"time"

	"log"
	"testing"

	"github.com/brudnak/hosted-tenant-rancher/util"
	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/assert"
)

func TestCreateHost(t *testing.T) {
	TestHostInfrastructureCreate(t)
	TestInstallHostRancher(t)
}

func TestHostInfrastructureCreate(t *testing.T) {

	var dataStoreEndpoint string
	var dataStorePassword string
	var firstPublicIP string
	var secondPlublicIP string
	var rancherURL string

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../basetf/hosted",
		NoColor:      true,
	})

	// defer terraform.Destroy(t, terraformOptions)

	terraform.InitAndApply(t, terraformOptions)
	firstPublicIP = terraform.Output(t, terraformOptions, "server_ip")
	secondPlublicIP = terraform.Output(t, terraformOptions, "server_ip2")
	dataStoreEndpoint = terraform.Output(t, terraformOptions, "db_endpoint")
	dataStorePassword = terraform.Output(t, terraformOptions, "db_password")
	rancherURL = terraform.Output(t, terraformOptions, "rancher_url")

	validatedFirstIP := util.CheckIPAddress(firstPublicIP)
	validatedSecondIP := util.CheckIPAddress(secondPlublicIP)

	assert.Equal(t, "valid", validatedFirstIP)
	assert.Equal(t, "valid", validatedSecondIP)

	firstServerCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | sh -s - server --token=SECRET --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, dataStorePassword, dataStoreEndpoint, rancherURL, firstPublicIP)

	var _ = util.RunCommand(firstServerCommand, firstPublicIP)

	token := util.RunCommand("sudo cat /var/lib/rancher/k3s/server/token", firstPublicIP)
	serverKubeConfig := util.RunCommand("sudo cat /etc/rancher/k3s/k3s.yaml", firstPublicIP)

	log.Println("start 60 second sleep")
	time.Sleep(60 * time.Second)
	log.Println("end 60 second sleep")

	secondServerCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | sh -s - server --token=%s --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, token, dataStorePassword, dataStoreEndpoint, rancherURL, secondPlublicIP)
	var _ = util.RunCommand(secondServerCommand, secondPlublicIP)

	log.Println("start 60 second sleep")
	time.Sleep(60 * time.Second)
	log.Println("end 60 second sleep")

	actualNodeCount := util.RunCommand("sudo k3s kubectl get nodes | wc -l", firstPublicIP)
	assert.Equal(t, "3", actualNodeCount)

	kubeConf := []byte(serverKubeConfig)

	configIP := fmt.Sprintf("https://%s:6443", firstPublicIP)
	output := bytes.Replace(kubeConf, []byte("https://127.0.0.1:6443"), []byte(configIP), -1)

	err := os.WriteFile("../config/kube_config.yml", output, 0644)
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
