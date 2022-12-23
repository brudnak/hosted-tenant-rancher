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
var configIp string

func TestHostInfrastructureCreate(t *testing.T) {

	viper.AddConfigPath("../../")
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

	actualHostNodeCount, _ := tools.SetupK3S(infra1MysqlPassword, infra1MysqlEndpoint, infra1RancherURL, infra1Server1IPAddress, infra1Server2IPAddress, "host")
	actualTenantNodeCount, tenantIp := tools.SetupK3S(infra2MysqlPassword, infra2MysqlEndpoint, infra2RancherURL, infra2Server1IPAddress, infra2Server2IPAddress, "tenant")

	configIp = tenantIp

	expectedNodeCount := 2

	assert.Equal(t, expectedNodeCount, actualHostNodeCount)
	assert.Equal(t, expectedNodeCount, actualTenantNodeCount)

	t.Run("install host rancher", TestInstallHostRancher)

	hostUrl = infra1RancherURL
	password = viper.GetString("rancher.bootstrap_password")

	time.Sleep(30 * time.Second)

	//t.Run("setup rancher import", TestSetupImport)
	t.Run("install tenant rancher", TestInstallTenantRancher)

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

func TestSetupImport(t *testing.T) {
	var tools toolkit.Tools
	tools.SetupImport(hostUrl, password, configIp)

	err := os.Setenv("KUBECONFIG", "theconfig.yml")
	if err != nil {
		log.Println("error setting env", err)
	}

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../modules/kubectl",
		NoColor:      true,
	})
	terraform.InitAndApply(t, terraformOptions)
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

	terraform.Destroy(t, terraformOptions)

	var tools toolkit.Tools

	// Kubeconfig files
	tools.RemoveFile("../../host.yml")
	tools.RemoveFile("../../tenant.yml")

	// Helm Host cleanup
	tools.RemoveFolder("../modules/helm/host/.terraform")
	tools.RemoveFile("../modules/helm/host/.terraform.lock.hcl")
	tools.RemoveFile("../modules/helm/host/terraform.tfstate")
	tools.RemoveFile("../modules/helm/host/terraform.tfstate.backup")
	tools.RemoveFile("../modules/helm/host/terraform.tfvars")

	// Helm Tenant Cleanup
	tools.RemoveFolder("../modules/helm/tenant/.terraform")
	tools.RemoveFile("../modules/helm/tenant/.terraform.lock.hcl")
	tools.RemoveFile("../modules/helm/tenant/terraform.tfstate")
	tools.RemoveFile("../modules/helm/tenant/terraform.tfstate.backup")
	tools.RemoveFile("../modules/helm/tenant/terraform.tfvars")

	// Kubectl Cleanup
	tools.RemoveFolder("../modules/kubectl/.terraform")
	tools.RemoveFile("../modules/kubectl/.terraform.lock.hcl")
	tools.RemoveFile("../modules/kubectl/terraform.tfstate")
	tools.RemoveFile("../modules/kubectl/terraform.tfstate.backup")
	tools.RemoveFile("../modules/kubectl/terraform.tfvars")
	tools.RemoveFile("../modules/kubectl/theconfig.yml")

	// AWS Cleanup
	defer tools.RemoveFolder("../modules/aws/.terraform")
	defer tools.RemoveFile("../modules/aws/.terraform.lock.hcl")
	defer tools.RemoveFile("../modules/aws/terraform.tfstate")
	defer tools.RemoveFile("../modules/aws/terraform.tfstate.backup")
}
