package test

import (
	"errors"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	toolkit "github.com/brudnak/hosted-tenant-rancher/tools"
	"github.com/brudnak/hosted-tenant-rancher/tools/hcl"
	"github.com/spf13/viper"
	"log"
	"os"
	"testing"
	"time"

	"github.com/gruntwork-io/terratest/modules/terraform"
)

var adminToken string
var currentTenantIndex int
var hostUrl string
var adminPassword string
var configIps []string
var tools toolkit.Tools

const (
	tfVars        = "terraform.tfvars"
	tfState       = "terraform.tfstate"
	tfStateBackup = "terraform.tfstate.backup"
)

func TestHosted(t *testing.T) {

	err := validatedRancherInstanceCount()
	if err != nil {
		log.Fatal("Error with rancher instance count: ", err)
	}

	err = checkS3ObjectExists(tfState)
	if err != nil {
		log.Fatal("Error checking if tfstate exists in s3: ", err)
	}

	createAWSVar()

	err = os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	if err != nil {
		log.Printf("error setting env: %v", err)
	}

	err = os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))
	if err != nil {
		log.Printf("error setting env: %v", err)
	}

	err = hcl.GenerateAWSMainTF(viper.GetInt("total_rancher_instances"))
	if err != nil {
		log.Println(err)
	}

	terraformOptions := &terraform.Options{
		TerraformDir: "../modules/aws",
		NoColor:      true,
		BackendConfig: map[string]interface{}{
			"bucket": viper.GetString("s3.bucket"),
			"key":    tfState,
			"region": viper.GetString("s3.region"),
		},
	}

	terraform.InitAndApply(t, terraformOptions)

	totalInstances := viper.GetInt("total_rancher_instances")

	var hostConfig toolkit.K3SConfig
	var tenantConfigs []toolkit.K3SConfig

	for i := 0; i < totalInstances; i++ {
		infraServer1IPAddress := terraform.Output(t, terraformOptions, fmt.Sprintf("infra%d_server1_ip", i+1))
		infraServer2IPAddress := terraform.Output(t, terraformOptions, fmt.Sprintf("infra%d_server2_ip", i+1))
		infraMysqlEndpoint := terraform.Output(t, terraformOptions, fmt.Sprintf("infra%d_mysql_endpoint", i+1))
		infraMysqlPassword := terraform.Output(t, terraformOptions, fmt.Sprintf("infra%d_mysql_password", i+1))
		infraRancherURL := terraform.Output(t, terraformOptions, fmt.Sprintf("infra%d_rancher_url", i+1))

		if i == 0 {

			// Set Host URL
			hostUrl = infraRancherURL
			// Host configuration
			hostConfig = toolkit.K3SConfig{
				DBPassword: infraMysqlPassword,
				DBEndpoint: infraMysqlEndpoint,
				RancherURL: infraRancherURL,
				Node1IP:    infraServer1IPAddress,
				Node2IP:    infraServer2IPAddress,
			}
		} else {
			// Tenant configurations
			tenantConfig := toolkit.K3SConfig{
				DBPassword: infraMysqlPassword,
				DBEndpoint: infraMysqlEndpoint,
				RancherURL: infraRancherURL,
				Node1IP:    infraServer1IPAddress,
				Node2IP:    infraServer2IPAddress,
			}
			tenantConfigs = append(tenantConfigs, tenantConfig)
		}
	}

	tools.K3SHostInstall(hostConfig)
	t.Run("install host rancher", TestInstallHostRancher)

	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err = viper.ReadInConfig()
	if err != nil {
		log.Println("error reading config:", err)
	}

	adminPassword = viper.GetString("rancher.bootstrap_password")
	adminToken, err = tools.CreateToken(hostUrl, adminPassword)
	if err != nil {
		log.Fatal("error creating token:", err)
	}

	err = tools.CallBashScript(hostUrl, adminToken)
	if err != nil {
		log.Println("error calling bash script", err)
	}

	log.Printf("Host Rancher https://%s", hostConfig.RancherURL)

	for i, tenantConfig := range tenantConfigs {
		tenantIndex := i + 1

		err := createTenantDirectories(tenantIndex)
		if err != nil {
			t.Fatalf("failed to create tenant directories: %v", err)
		}

		err = tools.GenerateKubectlTenantConfig(tenantIndex)
		if err != nil {
			t.Fatalf("failed to generate kubectl tenant config: %v", err)
		}

		err = tools.GenerateHelmTenantConfig(tenantIndex)
		if err != nil {
			t.Fatalf("failed to generate helm tenant config: %v", err)
		}

		tenantIp := tools.K3STenantInstall(tenantConfig, tenantIndex)
		configIps = append(configIps, tenantIp)

		currentTenantIndex = tenantIndex
		t.Run(fmt.Sprintf("setup rancher import for tenant %d", tenantIndex), func(t *testing.T) {
			TestSetupImport(t)
		})
		t.Run(fmt.Sprintf("install tenant rancher %d", tenantIndex), func(t *testing.T) {
			TestInstallTenantRancher(t)
		})

		log.Printf("Tenant Rancher %d https://%s", tenantIndex, tenantConfig.RancherURL)
	}

	log.Printf("Host Rancher https://%s", hostConfig.RancherURL)
	for i, tenantConfig := range tenantConfigs {
		log.Printf("Tenant Rancher %d https://%s", i+1, tenantConfig.RancherURL)
	}
}

func TestInstallHostRancher(t *testing.T) {

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../modules/helm/host",
		NoColor:      true,
	})

	terraform.InitAndApply(t, terraformOptions)
}

func TestUpgradeRancher(t *testing.T) {

	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()
	if err != nil {
		log.Println("error reading config:", err)
	}

	path := viper.GetString("upgrade.path")

	cleanupPath := fmt.Sprintf("../modules/helm/%s/%s", path, tfVars)
	cleanupFiles(cleanupPath)

	originalPath := fmt.Sprintf("../modules/helm/%s/upgrade.tfvars", path)
	newPath := fmt.Sprintf("../modules/helm/%s/%s", path, tfVars)

	e := os.Rename(originalPath, newPath)
	if e != nil {
		log.Fatal(e)
	}

	tfDirPath := fmt.Sprintf("../modules/helm/%s", path)

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: tfDirPath,
		NoColor:      true,
	})

	terraform.InitAndApply(t, terraformOptions)
}

func TestSetupImport(t *testing.T) {

	tenantIndex := currentTenantIndex
	configIp := configIps[tenantIndex-1]

	time.Sleep(120 * time.Second)
	tools.SetupImport(hostUrl, adminToken, configIp, tenantIndex)

	tenantKubeConfigPath := fmt.Sprintf("../modules/kubectl/tenant-%d/tenant_kube_config.yml", tenantIndex)
	err := os.Setenv("KUBECONFIG", tenantKubeConfigPath)
	if err != nil {
		log.Println("error setting env", err)
	}

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: fmt.Sprintf("../modules/kubectl/tenant-%d", tenantIndex),
		NoColor:      true,
	})
	terraform.InitAndApply(t, terraformOptions)
}

func TestInstallTenantRancher(t *testing.T) {
	time.Sleep(120 * time.Second)
	tenantIndex := currentTenantIndex
	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: fmt.Sprintf("../modules/helm/tenant-%d", tenantIndex),
		NoColor:      true,
	})
	terraform.InitAndApply(t, terraformOptions)
}

func TestJenkinsCleanup(t *testing.T) {
	createAWSVar()

	err := os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	if err != nil {
		log.Printf("error setting env: %v", err)
	}

	err = os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))
	if err != nil {
		log.Printf("error setting env: %v", err)
	}

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: "../modules/aws",
		NoColor:      true,
		BackendConfig: map[string]interface{}{
			"bucket": viper.GetString("s3.bucket"),
			"key":    tfState,
			"region": viper.GetString("s3.region"),
		},
	})
	terraform.Init(t, terraformOptions)
	terraform.Destroy(t, terraformOptions)
	err = deleteS3Object(viper.GetString("s3.bucket"), tfState)
	if err != nil {
		log.Printf("error deleting s3 object: %s", err)
	}
}

func TestHostCleanup(t *testing.T) {
	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{
		TerraformDir: "../modules/aws",
		NoColor:      true,
	})

	createAWSVar()
	err := os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	if err != nil {
		log.Printf("error setting env: %v", err)
	}

	err = os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))
	if err != nil {
		log.Printf("error setting env: %v", err)
	}

	terraform.Destroy(t, terraformOptions)

	filePaths := []string{
		"../../host.yml",
		"../modules/helm/host/.terraform.lock.hcl",
		"../modules/helm/host/" + tfState,
		"../modules/helm/host/" + tfStateBackup,
		"../modules/helm/host/" + tfVars,
		"../modules/helm/host/upgrade.tfvars",
		"../modules/aws/.terraform.lock.hcl",
		"../modules/aws/" + tfState,
		"../modules/aws/" + tfStateBackup,
		"../modules/aws/" + tfVars,
	}

	folderPaths := []string{
		"../modules/helm/host/.terraform",
		"../modules/aws/.terraform",
	}

	cleanupFiles(filePaths...)
	cleanupFolders(folderPaths...)
	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")

	err = viper.ReadInConfig()
	if err != nil {
		log.Println("error reading config:", err)
	}

	err = deleteS3Object(viper.GetString("s3.bucket"), tfState)
	if err != nil {
		log.Printf("error deleting s3 object: %s", err)
	}

	err = hcl.CleanupTerraformConfig()
	if err != nil {
		log.Printf("error cleaning up main.tf and dirs: %s", err)
	}
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
		viper.GetString("tf_vars.aws_ec2_instance_type"),
	)
}

// deleteS3Object deletes an object from a specified S3 bucket
func deleteS3Object(bucket string, item string) error {

	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()
	if err != nil {
		log.Println("Error reading config")
		return err
	}

	err = os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	if err != nil {
		log.Println("Error setting env")
		return err
	}

	err = os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))
	if err != nil {
		log.Println("Error setting env")
		return err
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

func validatedRancherInstanceCount() error {

	minRancherInstances := 2
	maxRancherInstances := 4
	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()
	if err != nil {
		return fmt.Errorf("error reading config: %v", err)
	}

	totalRancherInstances := viper.GetInt("total_rancher_instances")

	if totalRancherInstances > maxRancherInstances {
		return fmt.Errorf("can not creaet more than %v rancher instances", maxRancherInstances)
	}

	if totalRancherInstances < minRancherInstances {
		return fmt.Errorf("must have at least %v rancher instances", minRancherInstances)
	}

	return nil
}

func checkS3ObjectExists(item string) error {
	viper.AddConfigPath("../../")
	viper.SetConfigName("config")
	viper.SetConfigType("yml")
	err := viper.ReadInConfig()

	err = os.Setenv("AWS_ACCESS_KEY_ID", viper.GetString("tf_vars.aws_access_key"))
	if err != nil {
		log.Println("Error setting env")
		return err
	}

	err = os.Setenv("AWS_SECRET_ACCESS_KEY", viper.GetString("tf_vars.aws_secret_key"))
	if err != nil {
		log.Println("Error setting env")
		return err
	}

	sess, _ := session.NewSession(&aws.Config{
		Region: aws.String(viper.GetString("s3.region"))},
	)

	bucket := viper.GetString("s3.bucket")

	svc := s3.New(sess)

	_, err = svc.HeadObject(&s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(item)})
	if err != nil {
		// If the error is due to the file not existing, that's fine, and we return nil.
		var aErr awserr.Error
		if errors.As(err, &aErr) {
			switch aErr.Code() {
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

func createTenantDirectories(tenantIndex int) error {
	kubectlDir := fmt.Sprintf("../modules/kubectl/tenant-%d", tenantIndex)
	helmDir := fmt.Sprintf("../modules/helm/tenant-%d", tenantIndex)

	err := os.MkdirAll(kubectlDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create kubectl directory for tenant %d: %v", tenantIndex, err)
	}

	err = os.MkdirAll(helmDir, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create helm directory for tenant %d: %v", tenantIndex, err)
	}

	return nil
}
