package test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/viper"
)

func validateHostedConfiguration(totalInstances int, helmCommands []string, plans []*RancherResolvedPlan) error {
	if totalInstances < 2 {
		return fmt.Errorf("total_rancher_instances must be at least 2 (1 host + 1 tenant)")
	}
	if totalInstances > 4 {
		return fmt.Errorf("total_rancher_instances cannot exceed 4")
	}
	if len(helmCommands) != totalInstances {
		return fmt.Errorf("rancher.helm_commands has %d entries but total_rancher_instances is %d", len(helmCommands), totalInstances)
	}
	if err := validateResolvedHelmCommands(helmCommands); err != nil {
		return err
	}
	return validatePinnedK3SArtifacts(plans)
}

func validateResolvedHelmCommands(helmCommands []string) error {
	for i, helmCommand := range helmCommands {
		if !strings.Contains(helmCommand, "helm install") && !strings.Contains(helmCommand, "helm upgrade") {
			return fmt.Errorf("helm command %d is not an install/upgrade command", i+1)
		}
		requiredFlags := []string{
			"--set hostname=",
			"--set bootstrapPassword=",
			"--set agentTLSMode=system-store",
		}
		for _, requiredFlag := range requiredFlags {
			if !strings.Contains(helmCommand, requiredFlag) {
				return fmt.Errorf("helm command %d is missing %s", i+1, requiredFlag)
			}
		}
	}
	return nil
}

func validateLocalToolingPreflight(helmCommands []string) error {
	requiredCommands := []string{"kubectl", "helm", "terraform"}
	for _, commandName := range requiredCommands {
		if _, err := exec.LookPath(commandName); err != nil {
			return fmt.Errorf("%s is required locally but was not found in PATH", commandName)
		}
	}

	if err := refreshHelmRepoIndexes(); err != nil {
		return err
	}

	helmRepoOutput, err := exec.Command("helm", "repo", "list").CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to run 'helm repo list': %w", err)
	}

	missingHelmRepos := findMissingHelmRepos(string(helmRepoOutput), helmCommands)
	if len(missingHelmRepos) > 0 {
		return fmt.Errorf("missing required Helm repos locally: %s", strings.Join(missingHelmRepos, ", "))
	}

	return nil
}

func refreshHelmRepoIndexes() error {
	if output, err := exec.Command("helm", "repo", "update").CombinedOutput(); err != nil {
		return fmt.Errorf("failed to run 'helm repo update': %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func validateSecretEnvironment() error {
	loadSecretEnvironmentFromZProfile()
	loadLegacySecretFallbacksFromConfig()

	requiredEnvVars := []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"}
	for _, envVar := range requiredEnvVars {
		if strings.TrimSpace(os.Getenv(envVar)) == "" {
			return fmt.Errorf("%s must be set in the environment", envVar)
		}
	}

	dockerHubUsername := strings.TrimSpace(os.Getenv("DOCKERHUB_USERNAME"))
	dockerHubPassword := strings.TrimSpace(os.Getenv("DOCKERHUB_PASSWORD"))
	if (dockerHubUsername == "") != (dockerHubPassword == "") {
		return fmt.Errorf("set both DOCKERHUB_USERNAME and DOCKERHUB_PASSWORD, or leave both unset")
	}

	return nil
}

func loadSecretEnvironmentFromZProfile() {
	desiredVars := []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "DOCKERHUB_USERNAME", "DOCKERHUB_PASSWORD"}
	missingVars := false
	for _, envVar := range desiredVars {
		if strings.TrimSpace(os.Getenv(envVar)) == "" {
			missingVars = true
			break
		}
	}
	if !missingVars {
		return
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}

	content, err := os.ReadFile(filepath.Join(homeDir, ".zprofile"))
	if err != nil {
		return
	}

	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.HasPrefix(line, "export ") {
			continue
		}

		parts := strings.SplitN(strings.TrimPrefix(line, "export "), "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		if !slices.Contains(desiredVars, key) || strings.TrimSpace(os.Getenv(key)) != "" {
			continue
		}

		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if value != "" {
			_ = os.Setenv(key, value)
		}
	}
}

func loadLegacySecretFallbacksFromConfig() {
	_ = ensureConfigLoaded()

	legacySecrets := map[string]string{
		"AWS_ACCESS_KEY_ID":     strings.TrimSpace(viper.GetString("tf_vars.aws_access_key")),
		"AWS_SECRET_ACCESS_KEY": strings.TrimSpace(viper.GetString("tf_vars.aws_secret_key")),
		"DOCKERHUB_USERNAME":    strings.TrimSpace(viper.GetString("dockerhub.username")),
		"DOCKERHUB_PASSWORD":    strings.TrimSpace(viper.GetString("dockerhub.password")),
	}

	for envVar, value := range legacySecrets {
		if strings.TrimSpace(os.Getenv(envVar)) == "" && value != "" {
			_ = os.Setenv(envVar, value)
		}
	}
}

func validatePinnedK3SArtifacts(plans []*RancherResolvedPlan) error {
	seen := map[string]bool{}
	for _, plan := range plans {
		if plan == nil {
			continue
		}

		installURL := buildK3SInstallScriptURL(plan.RecommendedK3S)
		dedupKey := installURL + "|" + strings.ToLower(plan.InstallScriptSHA256)
		if !seen[dedupKey] {
			seen[dedupKey] = true
			if err := validateRemoteSHA256(installURL, plan.InstallScriptSHA256); err != nil {
				return err
			}
		}

		if plan.AirgapImageSHA256 != "" {
			airgapURL := buildK3SAirgapImageURL(plan.RecommendedK3S)
			dedupKey = airgapURL + "|" + strings.ToLower(plan.AirgapImageSHA256)
			if !seen[dedupKey] {
				seen[dedupKey] = true
				if err := validateRemoteSHA256(airgapURL, plan.AirgapImageSHA256); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateRemoteSHA256(url, expected string) error {
	actual, err := resolveRemoteSHA256(url)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, expected) {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", url, expected, actual)
	}
	return nil
}

func findMissingHelmRepos(helmRepoListOutput string, helmCommands []string) []string {
	knownRepos := map[string]bool{}
	for _, line := range strings.Split(helmRepoListOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || strings.EqualFold(fields[0], "NAME") {
			continue
		}
		knownRepos[fields[0]] = true
	}

	missingRepos := map[string]bool{}
	for _, helmCommand := range helmCommands {
		fields := strings.Fields(helmCommand)
		for _, field := range fields {
			if !strings.Contains(field, "/") || strings.HasPrefix(field, "http://") || strings.HasPrefix(field, "https://") || strings.HasPrefix(field, "--") {
				continue
			}
			repoName := strings.SplitN(field, "/", 2)[0]
			if repoName != "" && repoName != "." && !knownRepos[repoName] {
				missingRepos[repoName] = true
			}
			break
		}
	}

	var missing []string
	for repoName := range missingRepos {
		missing = append(missing, repoName)
	}
	slices.Sort(missing)
	return missing
}
