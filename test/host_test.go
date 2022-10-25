package test

import (
	"fmt"
	"os"

	"log"
	"testing"

	"github.com/brudnak/hosted-tenant-rancher/util"
	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/assert"
)

func TestHostInfrastructureCreate(t *testing.T) {
	t.Parallel()

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
	log.Println(serverKubeConfig)

	secondServerCommand := fmt.Sprintf(`curl -sfL https://get.k3s.io | sh -s - server --token=%s --datastore-endpoint='mysql://tfadmin:%s@tcp(%s)/k3s' --tls-san %s --node-external-ip %s`, token, dataStorePassword, dataStoreEndpoint, rancherURL, secondPlublicIP)
	var _ = util.RunCommand(secondServerCommand, secondPlublicIP)

	actualNodeCount := util.RunCommand("sudo k3s kubectl get nodes | wc -l", firstPublicIP)
	assert.Equal(t, "2", actualNodeCount)

	kubeConf := []byte(serverKubeConfig)
	err := os.WriteFile("../config/kube_config.yml", kubeConf, 0644)
	if err != nil {
		panic(err)
	}
}

func TestHostCleanup(t *testing.T) {
	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: "../basetf/hosted",
		NoColor:      true,
	})
	terraform.Destroy(t, terraformOptions)
}
