package hcl

import (
	"fmt"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/spf13/viper"
	"github.com/zclconf/go-cty/cty"
	"io/ioutil"
	"log"
	"os"
	"strings"
)

func GenerateAWSMainTF(tenantInstances int) error {
	// Read the existing Terraform configuration file
	configFilePath := "../../terratest/modules/aws/main.tf"
	content, err := ioutil.ReadFile(configFilePath)
	if err != nil {
		return fmt.Errorf("failed to read Terraform config file: %v", err)
	}

	// Find the line where the dynamic content should be inserted
	insertLine := "# dynamic write happens below here"
	lines := strings.Split(string(content), "\n")
	insertIndex := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == insertLine {
			insertIndex = i
			break
		}
	}

	if insertIndex == -1 {
		return fmt.Errorf("insertion line not found in the Terraform config file")
	}

	// Create a new file to write the updated content
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Add the module blocks for each tenant instance
	for i := 0; i < tenantInstances; i++ {
		moduleBlock := rootBody.AppendNewBlock("module", []string{fmt.Sprintf("high-availability-infrastructure-%d", i+1)}).Body()
		moduleBlock.SetAttributeValue("source", cty.StringVal("./modules/k3s-ha"))
		moduleBlock.SetAttributeTraversal("aws_prefix", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_prefix"},
		})
		moduleBlock.SetAttributeTraversal("aws_access_key", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_access_key"},
		})
		moduleBlock.SetAttributeTraversal("aws_secret_key", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_secret_key"},
		})
		moduleBlock.SetAttributeTraversal("aws_vpc", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_vpc"},
		})
		moduleBlock.SetAttributeTraversal("aws_subnet_a", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_subnet_a"},
		})
		moduleBlock.SetAttributeTraversal("aws_subnet_b", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_subnet_b"},
		})
		moduleBlock.SetAttributeTraversal("aws_subnet_c", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_subnet_c"},
		})
		moduleBlock.SetAttributeTraversal("aws_ami", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_ami"},
		})
		moduleBlock.SetAttributeTraversal("aws_subnet_id", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_subnet_id"},
		})
		moduleBlock.SetAttributeTraversal("aws_security_group_id", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_security_group_id"},
		})
		moduleBlock.SetAttributeTraversal("aws_pem_key_name", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_pem_key_name"},
		})
		moduleBlock.SetAttributeTraversal("aws_rds_password", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_rds_password"},
		})
		moduleBlock.SetAttributeTraversal("aws_route53_fqdn", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_route53_fqdn"},
		})
		moduleBlock.SetAttributeTraversal("aws_ec2_instance_type", hcl.Traversal{
			hcl.TraverseRoot{Name: "var.aws_ec2_instance_type"},
		})
	}

	// Add the output blocks for each tenant instance
	for i := 0; i < tenantInstances; i++ {
		outputServer1IPBlock := rootBody.AppendNewBlock("output", []string{fmt.Sprintf("infra%d_server1_ip", i+1)}).Body()
		outputServer1IPBlock.SetAttributeTraversal("value", hcl.Traversal{
			hcl.TraverseRoot{Name: fmt.Sprintf("module.high-availability-infrastructure-%d", i+1)},
			hcl.TraverseAttr{Name: "server1_ip"},
		})

		outputServer2IPBlock := rootBody.AppendNewBlock("output", []string{fmt.Sprintf("infra%d_server2_ip", i+1)}).Body()
		outputServer2IPBlock.SetAttributeTraversal("value", hcl.Traversal{
			hcl.TraverseRoot{Name: fmt.Sprintf("module.high-availability-infrastructure-%d", i+1)},
			hcl.TraverseAttr{Name: "server2_ip"},
		})

		outputMySQLEndpointBlock := rootBody.AppendNewBlock("output", []string{fmt.Sprintf("infra%d_mysql_endpoint", i+1)}).Body()
		outputMySQLEndpointBlock.SetAttributeTraversal("value", hcl.Traversal{
			hcl.TraverseRoot{Name: fmt.Sprintf("module.high-availability-infrastructure-%d", i+1)},
			hcl.TraverseAttr{Name: "mysql_endpoint"},
		})

		outputMySQLPasswordBlock := rootBody.AppendNewBlock("output", []string{fmt.Sprintf("infra%d_mysql_password", i+1)}).Body()
		outputMySQLPasswordBlock.SetAttributeTraversal("value", hcl.Traversal{
			hcl.TraverseRoot{Name: fmt.Sprintf("module.high-availability-infrastructure-%d", i+1)},
			hcl.TraverseAttr{Name: "mysql_password"},
		})
		outputMySQLPasswordBlock.SetAttributeValue("sensitive", cty.BoolVal(true))

		outputRancherURLBlock := rootBody.AppendNewBlock("output", []string{fmt.Sprintf("infra%d_rancher_url", i+1)}).Body()
		outputRancherURLBlock.SetAttributeTraversal("value", hcl.Traversal{
			hcl.TraverseRoot{Name: fmt.Sprintf("module.high-availability-infrastructure-%d", i+1)},
			hcl.TraverseAttr{Name: "rancher_url"},
		})
	}

	// Generate the dynamic content
	dynamicContent := string(f.Bytes())

	// Insert the dynamic content into the existing file
	updatedLines := append(lines[:insertIndex+1], append([]string{dynamicContent}, lines[insertIndex+1:]...)...)
	updatedContent := strings.Join(updatedLines, "\n")

	// Write the updated content back to the file
	err = ioutil.WriteFile(configFilePath, []byte(updatedContent), 0644)
	if err != nil {
		return fmt.Errorf("failed to write Terraform config file: %v", err)
	}

	return nil
}

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

	// Read the existing Terraform configuration file
	configFilePath := "../../terratest/modules/aws/main.tf"
	content, err := ioutil.ReadFile(configFilePath)
	if err != nil {
		return fmt.Errorf("failed to read Terraform config file: %v", err)
	}

	// Find the line where the dynamic content starts
	cleanupLine := "# dynamic write happens below here"
	lines := strings.Split(string(content), "\n")
	cleanupIndex := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == cleanupLine {
			cleanupIndex = i
			break
		}
	}

	if cleanupIndex == -1 {
		return fmt.Errorf("cleanup line not found in the Terraform config file")
	}

	// Remove the content below the cleanup line
	updatedLines := lines[:cleanupIndex+1]
	updatedContent := strings.Join(updatedLines, "\n")

	// Write the updated content back to the file
	err = ioutil.WriteFile(configFilePath, []byte(updatedContent), 0644)
	if err != nil {
		return fmt.Errorf("failed to write Terraform config file: %v", err)
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

	_, err = tfVarsFile.Write(f.Bytes())
	if err != nil {
		fmt.Println(err)
		return
	}
}
