package test

import (
	toolkit "github.com/brudnak/hosted-tenant-rancher/tools"
	"github.com/brudnak/hosted-tenant-rancher/tools/hcl"
	"github.com/spf13/viper"
	"log"
	"os"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/assert"
)

var hostUrl string
var password string
var configIp string

var tools toolkit.Tools

func TestHostInfrastructureCreate(t *testing.T) {

	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()

	if err != nil {
		log.Println("error reading config:", err)
	}

	hcl.GenAwsVar(
		viper.GetString("tf_vars.aws_access_key"),
		viper.GetString("tf_vars.aws_secret_key"),
		viper.GetString("tf_vars.aws_prefix"),
		viper.GetString("tf_vars.aws_vpc"),
		viper.GetString("tf_vars.aws_subnet_a"),
		viper.GetString("tf_vars.aws_subnet_b"),
		viper.GetString("tf_vars.aws_subnet_c"),
		viper.GetString("tf_vars.aws_ami"),
		viper.GetString("tf_vars.aws_subnet_id"),
		viper.GetString("tf_vars.aws_security_group_id"),
		viper.GetString("tf_vars.aws_pem_key_name"),
		viper.GetString("tf_vars.aws_rds_password"),
		viper.GetString("tf_vars.aws_route53_fqdn"),
		viper.GetString("tf_vars.local_path_aws_pem"),
	)

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

	t.Run("setup rancher import", TestSetupImport)
	t.Run("install tenant rancher", TestInstallTenantRancher)

	log.Printf("Host Rancher https://%s", infra1RancherURL)
	log.Printf("Tenant Rancher https://%s", infra2RancherURL)
}

func TestInstallHostRancher(t *testing.T) {

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../modules/helm/host",
		NoColor:      true,
	})

	terraform.InitAndApply(t, terraformOptions)
}

func TestUpgradeHostRancher(t *testing.T) {

	tools.RemoveFile("../modules/helm/host/terraform.tfvars")
	originalPath := "../modules/helm/host/upgrade.tfvars"
	newPath := "../modules/helm/host/terraform.tfvars"
	e := os.Rename(originalPath, newPath)
	if e != nil {
		log.Fatal(e)
	}

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../modules/helm/host",
		NoColor:      true,
	})

	terraform.InitAndApply(t, terraformOptions)
}

func TestSetupImport(t *testing.T) {

	tools.WorkAround(hostUrl, password)
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

func TestUpgradeTenantRancher(t *testing.T) {

	tools.RemoveFile("../modules/helm/tenant/terraform.tfvars")
	originalPath := "../modules/helm/tenant/upgrade.tfvars"
	newPath := "../modules/helm/tenant/terraform.tfvars"
	e := os.Rename(originalPath, newPath)
	if e != nil {
		log.Fatal(e)
	}

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

	// Kubeconfig files
	tools.RemoveFile("../../host.yml")
	tools.RemoveFile("../../tenant.yml")

	// Helm Host cleanup
	tools.RemoveFolder("../modules/helm/host/.terraform")
	tools.RemoveFile("../modules/helm/host/.terraform.lock.hcl")
	tools.RemoveFile("../modules/helm/host/terraform.tfstate")
	tools.RemoveFile("../modules/helm/host/terraform.tfstate.backup")
	tools.RemoveFile("../modules/helm/host/terraform.tfvars")
	tools.RemoveFile("../modules/helm/host/upgrade.tfvars")

	// Helm Tenant Cleanup
	tools.RemoveFolder("../modules/helm/tenant/.terraform")
	tools.RemoveFile("../modules/helm/tenant/.terraform.lock.hcl")
	tools.RemoveFile("../modules/helm/tenant/terraform.tfstate")
	tools.RemoveFile("../modules/helm/tenant/terraform.tfstate.backup")
	tools.RemoveFile("../modules/helm/tenant/terraform.tfvars")
	tools.RemoveFile("../modules/helm/tenant/upgrade.tfvars")

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
	defer tools.RemoveFile("../modules/aws/terraform.tfvars")
}
