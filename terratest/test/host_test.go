package test

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/brudnak/hosted-tenant-rancher/tools/hcl"
	"log"
	"os"
	"testing"

	toolkit "github.com/brudnak/hosted-tenant-rancher/tools"
	"github.com/spf13/viper"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/assert"
)

var hostUrl string
var password string
var configIp string

var tools toolkit.Tools

func TestCreateHostedTenantRancher(t *testing.T) {

	err := checkS3ObjectExists("terraform.tfstate")
	if err != nil {
		log.Fatal("Error checking if tfstate exists in s3:", err)
	}

	createAWSVar()
	os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))
	terraformOptions := &terraform.Options{
		TerraformDir: "../modules/aws",
		NoColor:      true,
		BackendConfig: map[string]interface{}{
			"bucket": viper.GetString("s3.bucket"),
			"key":    "terraform.tfstate",
			"region": viper.GetString("s3.region"),
		},
	}

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

	host := toolkit.K3SConfig{
		DBPassword: infra1MysqlPassword,
		DBEndpoint: infra1MysqlEndpoint,
		RancherURL: infra1RancherURL,
		Node1IP:    infra1Server1IPAddress,
		Node2IP:    infra1Server2IPAddress,
	}

	tenant := toolkit.K3SConfig{
		DBPassword: infra2MysqlPassword,
		DBEndpoint: infra2MysqlEndpoint,
		RancherURL: infra2RancherURL,
		Node1IP:    infra2Server1IPAddress,
		Node2IP:    infra2Server2IPAddress,
	}

	actualHostNodeCount, _ := tools.K3SHostInstall(host)
	actualTenantNodeCount, tenantIp := tools.K3STenantInstall(tenant)

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

	cleanupFiles("../modules/helm/host/terraform.tfvars")
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

	token := tools.CreateToken(hostUrl, password)
	err := tools.CallBashScript(hostUrl, token)
	if err != nil {
		log.Println("error calling bash script", err)
	}
	tools.SetupImport(hostUrl, password, configIp)

	err = os.Setenv("KUBECONFIG", "theconfig.yml")
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

	cleanupFiles("../modules/helm/tenant/terraform.tfvars")
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

func TestJenkinsCleanup(t *testing.T) {
	createAWSVar()
	os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))
	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: "../modules/aws",
		NoColor:      true,
		BackendConfig: map[string]interface{}{
			"bucket": viper.GetString("s3.bucket"),
			"key":    "terraform.tfstate",
			"region": viper.GetString("s3.region"),
		},
	})
	terraform.Init(t, terraformOptions)
	terraform.Destroy(t, terraformOptions)
	defer deleteS3Object(viper.GetString("s3.bucket"), "terraform.tfstate")
}

func TestHostCleanup(t *testing.T) {
	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: "../modules/aws",
		NoColor:      true,
	})

	terraform.Destroy(t, terraformOptions)

	filepaths := []string{
		"../../host.yml",
		"../../tenant.yml",
		"../modules/helm/host/.terraform.lock.hcl",
		"../modules/helm/host/terraform.tfstate",
		"../modules/helm/host/terraform.tfstate.backup",
		"../modules/helm/host/terraform.tfvars",
		"../modules/helm/host/upgrade.tfvars",
		"../modules/helm/tenant/.terraform.lock.hcl",
		"../modules/helm/tenant/terraform.tfstate",
		"../modules/helm/tenant/terraform.tfstate.backup",
		"../modules/helm/tenant/terraform.tfvars",
		"../modules/helm/tenant/upgrade.tfvars",
		"../modules/kubectl/.terraform.lock.hcl",
		"../modules/kubectl/terraform.tfstate",
		"../modules/kubectl/terraform.tfstate.backup",
		"../modules/kubectl/terraform.tfvars",
		"../modules/kubectl/theconfig.yml",
		"../modules/aws/.terraform.lock.hcl",
		"../modules/aws/terraform.tfstate",
		"../modules/aws/terraform.tfstate.backup",
		"../modules/aws/terraform.tfvars",
	}

	folderpaths := []string{
		"../modules/helm/host/.terraform",
		"../modules/helm/tenant/.terraform",
		"../modules/kubectl/.terraform",
		"../modules/aws/.terraform",
	}

	cleanupFiles(filepaths...)
	cleanupFolders(folderpaths...)
}

func cleanupFiles(paths ...string) {
	for _, path := range paths {
		err := tools.RemoveFile(path)
		if err != nil {
			log.Println("error removing file", err)
		}
	}
}

func cleanupFolders(paths ...string) {
	for _, path := range paths {
		err := tools.RemoveFolder(path)
		if err != nil {
			log.Println("error removing folder", err)
		}
	}
}

func createAWSVar() {
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
	)
}

// deleteS3Object deletes an object from a specified S3 bucket
func deleteS3Object(bucket string, item string) error {

	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()

	os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))

	if err != nil {
		log.Println("error reading config:", err)
	}

	sess, _ := session.NewSession(&aws.Config{
		Region: aws.String(viper.GetString("s3.region"))},
	)

	svc := s3.New(sess)

	_, err = svc.DeleteObject(&s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(item)})
	if err != nil {
		return err
	}

	err = svc.WaitUntilObjectNotExists(&s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(item),
	})
	if err != nil {
		return err
	}

	return nil
}

func checkS3ObjectExists(item string) error {
	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()

	os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))

	if err != nil {
		log.Println("error reading config:", err)
	}

	sess, _ := session.NewSession(&aws.Config{
		Region: aws.String(viper.GetString("s3.region"))},
	)

	bucket := viper.GetString("s3.bucket")

	svc := s3.New(sess)

	_, err = svc.HeadObject(&s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(item)})
	if err != nil {
		// If the error is due to the file not existing, that's fine, and we return nil.
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case s3.ErrCodeNoSuchKey, "NotFound":
				return nil
			}
		}
		// Otherwise, we return the error as it might be due to a network issue or something else.
		return err
	}

	// If we get to this point, it means the file exists, so we log an error message and exit the program.
	log.Fatalf("A tfstate file already exists in bucket %s. Please clean up the old hosted/tenant environment before creating a new one.", bucket)
	return nil
}
