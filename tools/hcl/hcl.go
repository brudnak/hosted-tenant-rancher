package hcl

import (
	"fmt"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/spf13/viper"
	"github.com/zclconf/go-cty/cty"
	"log"
	"os"
)

func CleanupTerraformConfig() error {
	tenantInstances := viper.GetInt("total_rancher_instances")
	for i := 0; i < tenantInstances; i++ {
		tenantIndex := i + 1
		kubectlDir := fmt.Sprintf("../modules/kubectl/tenant-%d", tenantIndex)
		helmDir := fmt.Sprintf("../modules/helm/tenant-%d", tenantIndex)

		err := os.RemoveAll(kubectlDir)
		if err != nil {
			log.Printf("failed to remove kubectl directory for tenant %d: %v", tenantIndex, err)
		}

		err = os.RemoveAll(helmDir)
		if err != nil {
			log.Printf("failed to remove helm directory for tenant %d: %v", tenantIndex, err)
		}
	}

	return nil
}

func GenerateKubectlTfVar(configIp string, manifestUrl string, tenantIndex int) {
	f := hclwrite.NewEmptyFile()

	tfVarsFile, err := os.Create(fmt.Sprintf("../../terratest/modules/kubectl/tenant-%d/terraform.tfvars", tenantIndex))
	if err != nil {
		fmt.Println(err)
		return
	}

	rootBody := f.Body()

	rootBody.SetAttributeValue("config_ip", cty.StringVal(configIp))
	rootBody.SetAttributeValue("manifest_url", cty.StringVal(manifestUrl))

	_, err = tfVarsFile.Write(f.Bytes())
	if err != nil {
		fmt.Println(err)
		return
	}
}

func GenAwsVar(
	accessKey,
	secretKey,
	awsPrefix,
	awsVpc,
	subnetA,
	subnetB,
	subnetC,
	awsAmi,
	subnetId,
	securityGroupId,
	pemKeyName,
	awsRdsPassword,
	route53Fqdn,
	instanceTypeSize string) {

	f := hclwrite.NewEmptyFile()

	tfVarsFile, err := os.Create("../../terratest/modules/aws/terraform.tfvars")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer tfVarsFile.Close()

	rootBody := f.Body()

	rootBody.SetAttributeValue("aws_access_key", cty.StringVal(accessKey))
	rootBody.SetAttributeValue("aws_secret_key", cty.StringVal(secretKey))
	rootBody.SetAttributeValue("aws_prefix", cty.StringVal(awsPrefix))
	rootBody.SetAttributeValue("aws_vpc", cty.StringVal(awsVpc))
	rootBody.SetAttributeValue("aws_subnet_a", cty.StringVal(subnetA))
	rootBody.SetAttributeValue("aws_subnet_b", cty.StringVal(subnetB))
	rootBody.SetAttributeValue("aws_subnet_c", cty.StringVal(subnetC))
	rootBody.SetAttributeValue("aws_ami", cty.StringVal(awsAmi))
	rootBody.SetAttributeValue("aws_subnet_id", cty.StringVal(subnetId))
	rootBody.SetAttributeValue("aws_security_group_id", cty.StringVal(securityGroupId))
	rootBody.SetAttributeValue("aws_pem_key_name", cty.StringVal(pemKeyName))
	rootBody.SetAttributeValue("aws_rds_password", cty.StringVal(awsRdsPassword))
	rootBody.SetAttributeValue("aws_route53_fqdn", cty.StringVal(route53Fqdn))
	rootBody.SetAttributeValue("aws_ec2_instance_type", cty.StringVal(instanceTypeSize))
	rootBody.SetAttributeValue("total_rancher_instances", cty.NumberIntVal(int64(viper.GetInt("total_rancher_instances"))))

	_, err = tfVarsFile.Write(f.Bytes())
	if err != nil {
		fmt.Println(err)
		return
	}
}
