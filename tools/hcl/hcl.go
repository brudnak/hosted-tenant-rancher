package hcl

import (
	"fmt"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
	"os"
)

func GenerateKubectlTfVar(configIp string, manifestUrl string) {
	f := hclwrite.NewEmptyFile()

	tfVarsFile, err := os.Create("../../terratest/modules/kubectl/terraform.tfvars")
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
