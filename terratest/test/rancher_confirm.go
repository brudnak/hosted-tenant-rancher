package test

import (
	"fmt"
	"log"
	"regexp"
	"strings"
)

func buildResolvedPlansDialogMessage(plans []*RancherResolvedPlan) string {
	sections := []string{"Continue with this hosted/tenant Rancher plan?"}

	for i, plan := range plans {
		if plan == nil {
			continue
		}

		section := []string{fmt.Sprintf("Instance %d", i+1)}
		if plan.RequestedVersion != "" {
			section = append(section, "Requested Rancher: "+plan.RequestedVersion)
		}
		if plan.ChartRepoAlias != "" {
			section = append(section, fmt.Sprintf("Selected chart: %s/rancher@%s", plan.ChartRepoAlias, plan.ChartVersion))
		}
		if plan.RecommendedK3S != "" {
			section = append(section, "Resolved K3s/K8s: "+plan.RecommendedK3S)
		}
		for j, helmCommand := range plan.HelmCommands {
			section = append(section, fmt.Sprintf("Helm command %d:", j+1), sanitizeHelmCommand(helmCommand))
		}
		sections = append(sections, strings.Join(section, "\n"))
	}

	return strings.Join(sections, "\n\n")
}

func logResolvedPlans(plans []*RancherResolvedPlan) {
	for i, plan := range plans {
		log.Printf("[resolver] Hosted/Tenant resolution summary for instance %d:", i+1)
		log.Printf("[resolver] Requested Rancher: %s", plan.RequestedVersion)
		log.Printf("[resolver] Resolved chart: %s/rancher@%s", plan.ChartRepoAlias, plan.ChartVersion)
		log.Printf("[resolver] Resolved K3s: %s", plan.RecommendedK3S)
		log.Printf("[resolver] Support matrix: %s", plan.SupportMatrixURL)
		log.Printf("[resolver] Installer SHA256: %s", plan.InstallScriptSHA256)
		if plan.AirgapImageSHA256 != "" {
			log.Printf("[resolver] Airgap SHA256: %s", plan.AirgapImageSHA256)
		}
		for _, explanation := range plan.Explanation {
			log.Printf("[resolver] Reason: %s", explanation)
		}
		for _, helmCommand := range plan.HelmCommands {
			log.Printf("[resolver] Helm command:\n%s", sanitizeHelmCommand(helmCommand))
		}
	}
}

func sanitizeHelmCommand(command string) string {
	pattern := regexp.MustCompile(`bootstrapPassword=[^\s\\]+`)
	return pattern.ReplaceAllString(command, "bootstrapPassword=<redacted>")
}
