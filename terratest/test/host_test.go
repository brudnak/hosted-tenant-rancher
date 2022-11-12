package test

import (
	"log"
	"os"
	"testing"
	"time"

	toolkit "github.com/brudnak/hosted-tenant-rancher/tools"
	"github.com/spf13/viper"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/assert"
)

var hostUrl string
var password string

func TestHostInfrastructureCreate(t *testing.T) {

	viper.AddConfigPath("../../config")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()

	if err != nil {
		log.Println("error reading config:", err)
	}

	var tools toolkit.Tools

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../modules/aws",
		NoColor:      true,
	})

	terraform.InitAndApply(t, terraformOptions)

	infra1Server1IPAddress := terraform.Output(t, terraformOptions, "infra1_server1_ip")
	infra1Server2IPAddress := terraform.Output(t, terraformOptions, "infra1_server2_ip")
	infra1MysqlEndpoint := terraform.Output(t, terraformOptions, "infra1_mysql_endpoint")
	infra1MysqlPassword := terraform.Output(t, terraformOptions, "infra1_mysql_password")
	infra1RancherURL := terraform.Output(t, terraformOptions, "infra1_rancher_url")

	infra2Server1IPAddress := terraform.Output(t, terraformOptions, "infra2_server1_ip")
	infra2Server2IPAddress := terraform.Output(t, terraformOptions, "infra2_server2_ip")
	infra2MysqlEndpoint := terraform.Output(t, terraformOptions, "infra2_mysql_endpoint")
	infra2MysqlPassword := terraform.Output(t, terraformOptions, "infra2_mysql_password")
	infra2RancherURL := terraform.Output(t, terraformOptions, "infra2_rancher_url")

	noneOneIPAddressValidationResult := tools.CheckIPAddress(infra1Server1IPAddress)
	nodeTwoIPAddressValidationResult := tools.CheckIPAddress(infra1Server2IPAddress)

	assert.Equal(t, "valid", noneOneIPAddressValidationResult)
	assert.Equal(t, "valid", nodeTwoIPAddressValidationResult)

	actualHostNodeCount := tools.SetupK3S(infra1MysqlPassword, infra1MysqlEndpoint, infra1RancherURL, infra1Server1IPAddress, infra1Server2IPAddress, "host")
	actualTenantNodeCount := tools.SetupK3S(infra2MysqlPassword, infra2MysqlEndpoint, infra2RancherURL, infra2Server1IPAddress, infra2Server2IPAddress, "tenant")

	expectedNodeCount := 2

	assert.Equal(t, expectedNodeCount, actualHostNodeCount)
	assert.Equal(t, expectedNodeCount, actualTenantNodeCount)

	t.Run("install host rancher", TestInstallHostRancher)

	hostUrl = infra1RancherURL
	password = viper.GetString("rancher.bootstrap_password")

	time.Sleep(30 * time.Second)

	t.Run("Importing k3s cluster into Rancher", TestCreateImportedCluster)

	// t.Run("install tenant rancher", TestInstallTenantRancher)
	log.Println("Rancher url", infra1RancherURL)
	log.Println("Rancher url", infra2RancherURL)
}

func TestInstallHostRancher(t *testing.T) {

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../modules/helm/host",
		NoColor:      true,
	})

	terraform.InitAndApply(t, terraformOptions)
}

func TestCreateImportedCluster(t *testing.T) {
	var tools toolkit.Tools
	adminToken := tools.CreateToken(hostUrl, password)
	log.Println(adminToken)
}

func TestInstallTenantRancher(t *testing.T) {

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../modules/helm/tenant",
		NoColor:      true,
	})

	terraform.InitAndApply(t, terraformOptions)
}

func TestHostCleanup(t *testing.T) {
	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: "../modules/aws",
		NoColor:      true,
	})

	err := os.Remove("../../kube/config/host.yml")
	if err != nil {
		log.Println(err)
	}

	err = os.Remove("../../kube/config/tenant.yml")
	if err != nil {
		log.Println(err)
	}

	err = os.Remove("../modules/helm/host/terraform.tfvars")
	if err != nil {
		log.Println(err)
	}

	err = os.Remove("../modules/helm/tenant/terraform.tfvars")
	if err != nil {
		log.Println(err)
	}

	terraform.Destroy(t, terraformOptions)
}
