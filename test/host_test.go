package test

import (
	"log"
	"net"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/stretchr/testify/assert"
)

func TestHostInfrastructureCreate(t *testing.T) {
	t.Parallel()

	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../basetf/hosted",
		NoColor:      true,
	})

	// defer terraform.Destroy(t, terraformOptions)

	terraform.InitAndApply(t, terraformOptions)
	pubIP1 := terraform.Output(t, terraformOptions, "public_ip_1")
	log.Println(pubIP1)
	checkedIP1 := checkIPAddress(pubIP1)

	assert.Equal(t, "valid", checkedIP1)
}

func TestHostCleanup(t *testing.T) {
	terraformOptions := terraform.WithDefaultRetryableErrors(t, &terraform.Options{

		TerraformDir: "../basetf/hosted",
		NoColor:      true,
	})
	terraform.Destroy(t, terraformOptions)
}

func checkIPAddress(ip string) string {
	if net.ParseIP(ip) == nil {
		return "invalid"
	} else {
		return "valid"
	}
}
