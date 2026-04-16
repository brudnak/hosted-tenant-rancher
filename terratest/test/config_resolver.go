package test

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	goversion "github.com/hashicorp/go-version"
	"github.com/spf13/viper"
	"golang.org/x/net/html"
)

type RancherResolvedPlan struct {
	Mode                string
	RequestedVersion    string
	RequestedDistro     string
	BuildType           string
	ResolvedDistro      string
	ChartRepoAlias      string
	ChartVersion        string
	RancherImage        string
	RancherImageTag     string
	AgentImage          string
	CompatibilityBase   string
	SupportMatrixURL    string
	RecommendedK3S      string
	InstallScriptSHA256 string
	AirgapImageSHA256   string
	HelmCommands        []string
	Explanation         []string
}

type helmSearchResult struct {
	Name       string `json:"name"`
	Version    string `json:"version"`
	AppVersion string `json:"app_version"`
}

func setupConfig(tb testing.TB) {
	tb.Helper()
	if err := ensureConfigLoaded(); err != nil {
		tb.Fatalf("failed to read config: %v", err)
	}
}

func ensureConfigLoaded() error {
	if viper.ConfigFileUsed() != "" {
		return nil
	}

	viper.SetConfigType("yml")
	viper.AddConfigPath("../../")

	for _, configName := range []string{"tool-config", "config"} {
		viper.SetConfigName(configName)
		if err := viper.ReadInConfig(); err == nil {
			return nil
		} else {
			var notFound viper.ConfigFileNotFoundError
			if !errors.As(err, &notFound) {
				return err
			}
		}
	}

	return fmt.Errorf("expected tool-config.yml at the repo root")
}

func getTotalRancherInstances() int {
	if total := viper.GetInt("total_rancher_instances"); total > 0 {
		return total
	}
	return viper.GetInt("total_has")
}

func prepareRancherConfiguration(totalInstances int) ([]*RancherResolvedPlan, error) {
	mode := strings.ToLower(strings.TrimSpace(viper.GetString("rancher.mode")))
	switch mode {
	case "", "manual":
		return prepareManualK3SPlans(totalInstances)
	case "auto":
		plans, err := resolveAutoRancherPlans(totalInstances)
		if err != nil {
			return nil, err
		}

		helmCommands := make([]string, 0, len(plans))
		k3sVersions := make([]string, 0, len(plans))
		installChecksums := map[string]string{}
		airgapChecksums := map[string]string{}

		for _, plan := range plans {
			helmCommands = append(helmCommands, plan.HelmCommands...)
			k3sVersions = append(k3sVersions, plan.RecommendedK3S)
			installChecksums[plan.RecommendedK3S] = plan.InstallScriptSHA256
			airgapChecksums[plan.RecommendedK3S] = plan.AirgapImageSHA256
		}

		viper.Set("rancher.helm_commands", helmCommands)
		viper.Set("k3s.versions", k3sVersions)
		viper.Set("k3s.install_script_sha256s", installChecksums)
		viper.Set("k3s.airgap_image_sha256s", airgapChecksums)
		return plans, nil
	default:
		return nil, fmt.Errorf("unsupported rancher.mode %q", mode)
	}
}

func prepareManualK3SPlans(totalInstances int) ([]*RancherResolvedPlan, error) {
	helmCommands := viper.GetStringSlice("rancher.helm_commands")
	if len(helmCommands) != totalInstances {
		return nil, fmt.Errorf("rancher.helm_commands has %d entries but total_rancher_instances is %d", len(helmCommands), totalInstances)
	}

	k3sVersions, err := getRequestedK3SVersions(totalInstances)
	if err != nil {
		return nil, err
	}

	plans := make([]*RancherResolvedPlan, 0, len(k3sVersions))
	for _, version := range k3sVersions {
		installChecksum, err := k3sChecksumForVersion("k3s.install_script_sha256s", "k3s.install_script_sha256", version)
		if err != nil {
			return nil, err
		}

		airgapChecksum := ""
		if viper.GetBool("k3s.preload_images") {
			airgapChecksum, err = k3sChecksumForVersion("k3s.airgap_image_sha256s", "k3s.airgap_image_sha256", version)
			if err != nil {
				return nil, err
			}
		}

		plans = append(plans, &RancherResolvedPlan{
			Mode:                "manual",
			RecommendedK3S:      version,
			InstallScriptSHA256: installChecksum,
			AirgapImageSHA256:   airgapChecksum,
		})
	}

	return plans, nil
}

func getRequestedK3SVersions(totalInstances int) ([]string, error) {
	requestedVersions := viper.GetStringSlice("k3s.versions")
	if len(requestedVersions) > 0 {
		if len(requestedVersions) != totalInstances {
			return nil, fmt.Errorf("k3s.versions has %d entries but total_rancher_instances is %d", len(requestedVersions), totalInstances)
		}

		out := make([]string, 0, len(requestedVersions))
		for i, version := range requestedVersions {
			version = strings.TrimSpace(version)
			if version == "" {
				return nil, fmt.Errorf("k3s.versions[%d] must not be empty", i)
			}
			out = append(out, version)
		}
		return out, nil
	}

	requestedVersion := strings.TrimSpace(viper.GetString("k3s.version"))
	if requestedVersion == "" {
		return nil, fmt.Errorf("set k3s.version for a single instance or k3s.versions with %d entries", totalInstances)
	}
	if totalInstances > 1 {
		return nil, fmt.Errorf("total_rancher_instances is %d, so k3s.versions must contain %d versions", totalInstances, totalInstances)
	}

	return []string{requestedVersion}, nil
}

func getRequestedRancherVersions(totalInstances int) ([]string, error) {
	requestedVersions := viper.GetStringSlice("rancher.versions")
	if len(requestedVersions) > 0 {
		if len(requestedVersions) != totalInstances {
			return nil, fmt.Errorf("rancher.versions has %d entries but total_rancher_instances is %d", len(requestedVersions), totalInstances)
		}

		out := make([]string, 0, len(requestedVersions))
		for i, version := range requestedVersions {
			version = normalizeVersionInput(version)
			if version == "" {
				return nil, fmt.Errorf("rancher.versions[%d] must not be empty", i)
			}
			out = append(out, version)
		}
		return out, nil
	}

	requestedVersion := normalizeVersionInput(viper.GetString("rancher.version"))
	if requestedVersion == "" {
		return nil, fmt.Errorf("set rancher.version for a single instance or rancher.versions with %d entries", totalInstances)
	}
	if totalInstances > 1 {
		return nil, fmt.Errorf("total_rancher_instances is %d, so rancher.versions must contain %d versions", totalInstances, totalInstances)
	}

	return []string{requestedVersion}, nil
}

func normalizeVersionInput(value string) string {
	value = strings.TrimSpace(value)
	return strings.TrimPrefix(value, "v")
}

func resolveAutoRancherPlans(totalInstances int) ([]*RancherResolvedPlan, error) {
	requestedVersions, err := getRequestedRancherVersions(totalInstances)
	if err != nil {
		return nil, err
	}

	if err := refreshHelmRepoIndexes(); err != nil {
		return nil, err
	}

	requestedDistro := strings.ToLower(strings.TrimSpace(viper.GetString("rancher.distro")))
	if requestedDistro == "" {
		requestedDistro = "auto"
	}

	bootstrapPassword := strings.TrimSpace(viper.GetString("rancher.bootstrap_password"))
	if bootstrapPassword == "" {
		return nil, fmt.Errorf("rancher.bootstrap_password must be set when rancher.mode=auto")
	}

	plans := make([]*RancherResolvedPlan, 0, len(requestedVersions))
	for _, requestedVersion := range requestedVersions {
		buildType, minorLine, err := classifyRancherVersion(requestedVersion)
		if err != nil {
			return nil, err
		}

		repoCandidates, resolvedDistro, explanation := chooseRancherSourceCandidates(requestedDistro, buildType)
		chartRepoAlias, chartVersion, compatibilityBase, err := resolveChartAndBaseline(repoCandidates, requestedVersion, minorLine, buildType)
		if err != nil {
			return nil, err
		}

		rancherImage, rancherImageTag, agentImage, imageExplanation := resolveImageSettings(requestedVersion, buildType, resolvedDistro)
		if buildType != "release" && chartVersion == requestedVersion {
			rancherImage = ""
			rancherImageTag = ""
			agentImage = ""
			explanation = append(explanation, fmt.Sprintf("Using exact chart match %s/rancher@%s, so no image override is needed", chartRepoAlias, chartVersion))
		}
		explanation = append(explanation, imageExplanation...)

		supportMatrixURL := buildSupportMatrixURL(compatibilityBase)
		highestK3SMinor, supportExplanation, err := resolveHighestSupportedK3SMinor(supportMatrixURL)
		if err != nil {
			return nil, err
		}
		explanation = append(explanation, supportExplanation)

		recommendedK3S, err := resolveLatestK3SPatch(highestK3SMinor)
		if err != nil {
			return nil, err
		}
		explanation = append(explanation, fmt.Sprintf("Selected %s as the latest available K3s patch in the supported v1.%d line", recommendedK3S, highestK3SMinor))

		installSHA, err := resolveRemoteSHA256(buildK3SInstallScriptURL(recommendedK3S))
		if err != nil {
			return nil, err
		}

		airgapSHA := ""
		if viper.GetBool("k3s.preload_images") {
			airgapSHA, err = resolveRemoteSHA256(buildK3SAirgapImageURL(recommendedK3S))
			if err != nil {
				return nil, err
			}
		}

		plans = append(plans, &RancherResolvedPlan{
			Mode:                "auto",
			RequestedVersion:    requestedVersion,
			RequestedDistro:     requestedDistro,
			BuildType:           buildType,
			ResolvedDistro:      resolvedDistro,
			ChartRepoAlias:      chartRepoAlias,
			ChartVersion:        chartVersion,
			RancherImage:        rancherImage,
			RancherImageTag:     rancherImageTag,
			AgentImage:          agentImage,
			CompatibilityBase:   compatibilityBase,
			SupportMatrixURL:    supportMatrixURL,
			RecommendedK3S:      recommendedK3S,
			InstallScriptSHA256: installSHA,
			AirgapImageSHA256:   airgapSHA,
			HelmCommands:        buildAutoHelmCommands(1, chartRepoAlias, chartVersion, bootstrapPassword, rancherImage, rancherImageTag, agentImage),
			Explanation:         explanation,
		})
	}

	return plans, nil
}

func confirmResolvedPlans(plans []*RancherResolvedPlan) error {
	if len(plans) == 0 || plans[0] == nil || plans[0].Mode == "manual" {
		return nil
	}

	logResolvedPlans(plans)

	if viper.GetBool("rancher.auto_approve") {
		log.Printf("[resolver] Auto-approve enabled, continuing without prompt")
		return nil
	}

	if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer tty.Close()
		if _, err := fmt.Fprint(tty, "Continue with this hosted/tenant Rancher plan? [y/N]: "); err != nil {
			return err
		}
		reader := bufio.NewReader(tty)
		response, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if isAffirmative(response) {
			return nil
		}
		return fmt.Errorf("user canceled resolved Rancher plans")
	}

	if runtime.GOOS == "darwin" {
		approved, err := confirmResolvedPlansWithOSDialog(plans)
		if err == nil {
			if approved {
				return nil
			}
			return fmt.Errorf("user canceled resolved Rancher plans")
		}
	}

	fmt.Print("Continue with this hosted/tenant Rancher plan? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if isAffirmative(response) {
		return nil
	}

	return fmt.Errorf("user canceled resolved Rancher plans")
}

func isAffirmative(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "y", "yes", "continue":
		return true
	default:
		return false
	}
}

func confirmResolvedPlansWithOSDialog(plans []*RancherResolvedPlan) (bool, error) {
	script := `on run argv
set planMessage to item 1 of argv
button returned of (display dialog planMessage buttons {"Cancel", "Continue"} default button "Continue" cancel button "Cancel" with title "Hosted/Tenant Rancher Plan")
end run`
	output, err := exec.Command("osascript", "-e", script, buildResolvedPlansDialogMessage(plans)).CombinedOutput()
	if err != nil {
		return false, err
	}

	return strings.Contains(string(output), "Continue"), nil
}

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

func resolveRemoteSHA256(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected HTTP status %d downloading %s", resp.StatusCode, url)
	}

	hasher := sha256.New()
	if _, err := io.Copy(hasher, resp.Body); err != nil {
		return "", fmt.Errorf("failed hashing %s: %w", url, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func k3sChecksumForVersion(mapKey, singleKey, version string) (string, error) {
	checksums := viper.GetStringMapString(mapKey)
	if checksum := strings.TrimSpace(checksums[version]); checksum != "" {
		return checksum, nil
	}
	if strings.TrimSpace(viper.GetString("k3s.version")) == version {
		if checksum := strings.TrimSpace(viper.GetString(singleKey)); checksum != "" {
			return checksum, nil
		}
	}
	return "", fmt.Errorf("%s.%s must be set", mapKey, version)
}

func classifyRancherVersion(version string) (string, string, error) {
	headPattern := regexp.MustCompile(`^\d+\.\d+-head$`)
	alphaPattern := regexp.MustCompile(`^\d+\.\d+\.\d+-alpha\d+$`)
	rcPattern := regexp.MustCompile(`^\d+\.\d+\.\d+-rc\d+$`)
	releasePattern := regexp.MustCompile(`^\d+\.\d+\.\d+$`)

	switch {
	case headPattern.MatchString(version):
		parts := strings.Split(version, "-")
		return "head", parts[0], nil
	case alphaPattern.MatchString(version):
		parts := strings.Split(version, "-")
		return "alpha", strings.Join(strings.Split(parts[0], ".")[:2], "."), nil
	case rcPattern.MatchString(version):
		parts := strings.Split(version, "-")
		return "rc", strings.Join(strings.Split(parts[0], ".")[:2], "."), nil
	case releasePattern.MatchString(version):
		return "release", strings.Join(strings.Split(version, ".")[:2], "."), nil
	default:
		return "", "", fmt.Errorf("unsupported rancher.version format %q", version)
	}
}

func chooseRancherSourceCandidates(requestedDistro, buildType string) ([]string, string, []string) {
	switch requestedDistro {
	case "prime":
		return []string{"rancher-prime"}, "prime", []string{"Prime distro was requested explicitly"}
	case "community":
		switch buildType {
		case "head":
			return []string{"optimus-rancher-latest"}, "community-staging", []string{"Head build requested, using community staging chart sources"}
		case "alpha":
			return []string{"optimus-rancher-alpha", "optimus-rancher-latest", "rancher-alpha", "rancher-latest"}, "community-staging", []string{"Alpha build requested, trying community alpha/staging chart sources first"}
		case "rc":
			return []string{"optimus-rancher-latest", "rancher-latest"}, "community-staging", []string{"RC build requested, trying community staging chart sources first"}
		default:
			return []string{"rancher-latest", "optimus-rancher-latest"}, "community", []string{"Released community build requested"}
		}
	default:
		switch buildType {
		case "head":
			return []string{"rancher-prime", "rancher-latest", "optimus-rancher-latest"}, "community-staging", []string{"Head build requested in auto mode, trying the latest released chart first and then community staging charts"}
		case "alpha":
			return []string{"rancher-prime", "rancher-latest", "optimus-rancher-alpha", "optimus-rancher-latest", "rancher-alpha"}, "community-staging", []string{"Alpha build requested in auto mode, trying the latest released chart first and then community alpha/staging sources"}
		case "rc":
			return []string{"rancher-prime", "rancher-latest", "optimus-rancher-latest"}, "community-staging", []string{"RC build requested in auto mode, trying the latest released chart first and then community staging charts"}
		default:
			return []string{"rancher-latest", "rancher-prime"}, "community", []string{"Released build requested in auto mode, trying community release sources first"}
		}
	}
}

func resolveChartAndBaseline(repoCandidates []string, requestedVersion, minorLine, buildType string) (string, string, string, error) {
	if globalExactMatch, err := findExactRequestedChartAcrossRepos(repoCandidates, requestedVersion); err == nil {
		compatibilityBase := requestedVersion
		if buildType != "release" {
			compatibilityBase, err = resolveCompatibilityBaseline(minorLine)
			if err != nil {
				compatibilityBase = requestedVersion
			}
		}
		return globalExactMatch.repoAlias, globalExactMatch.chartVersion, compatibilityBase, nil
	}

	var lastErr error
	var bestMatch *resolvedChartMatch
	for _, repoAlias := range repoCandidates {
		results, err := searchHelmRepoVersions(repoAlias)
		if err != nil {
			lastErr = err
			continue
		}
		if len(results) == 0 {
			continue
		}

		switch buildType {
		case "release":
			if hasChartVersion(results, requestedVersion) {
				recordResolvedChartMatch(&bestMatch, repoAlias, requestedVersion, requestedVersion, 0)
			}
		default:
			sameMinorRelease, sameMinorReleaseErr := findLatestMinorRelease(results, minorLine)
			compatibilityBase, baselineErr := resolveCompatibilityBaseline(minorLine)

			if hasChartVersion(results, requestedVersion) {
				if baselineErr != nil {
					compatibilityBase = requestedVersion
				}
				recordResolvedChartMatch(&bestMatch, repoAlias, requestedVersion, compatibilityBase, 0)
			}
			if sameMinorReleaseErr == nil {
				if baselineErr != nil {
					compatibilityBase = sameMinorRelease
				}
				recordResolvedChartMatch(&bestMatch, repoAlias, sameMinorRelease, compatibilityBase, 1)
			}
			if baselineErr == nil && hasChartVersion(results, compatibilityBase) {
				recordResolvedChartMatch(&bestMatch, repoAlias, compatibilityBase, compatibilityBase, 2)
			}
			lastErr = sameMinorReleaseErr
		}
	}

	if bestMatch != nil {
		return bestMatch.repoAlias, bestMatch.chartVersion, bestMatch.compatibilityBaseline, nil
	}
	if lastErr != nil {
		return "", "", "", lastErr
	}
	return "", "", "", fmt.Errorf("could not resolve a Rancher chart version for %s from repos %s", requestedVersion, strings.Join(repoCandidates, ", "))
}

type resolvedChartMatch struct {
	repoAlias             string
	chartVersion          string
	compatibilityBaseline string
	matchRank             int
}

func recordResolvedChartMatch(bestMatch **resolvedChartMatch, repoAlias, chartVersion, compatibilityBaseline string, matchRank int) {
	if *bestMatch == nil || matchRank < (*bestMatch).matchRank {
		*bestMatch = &resolvedChartMatch{
			repoAlias:             repoAlias,
			chartVersion:          chartVersion,
			compatibilityBaseline: compatibilityBaseline,
			matchRank:             matchRank,
		}
	}
}

func findExactRequestedChartAcrossRepos(repoCandidates []string, requestedVersion string) (*resolvedChartMatch, error) {
	globalResults, err := searchAllHelmRepoVersions()
	if err != nil {
		return nil, err
	}

	for _, repoAlias := range repoCandidates {
		for _, result := range globalResults {
			if result.Name != fmt.Sprintf("%s/rancher", repoAlias) {
				continue
			}
			if result.Version == requestedVersion || normalizeVersionInput(result.AppVersion) == requestedVersion {
				return &resolvedChartMatch{repoAlias: repoAlias, chartVersion: result.Version, matchRank: 0}, nil
			}
		}
	}

	return nil, fmt.Errorf("no exact chart match found across repos for Rancher %s", requestedVersion)
}

func resolveCompatibilityBaseline(minorLine string) (string, error) {
	baseline, err := resolveReleasedCompatibilityBaseline(minorLine)
	if err == nil {
		return baseline, nil
	}

	previousMinorLine, previousErr := previousRancherMinorLine(minorLine)
	if previousErr != nil {
		return "", err
	}

	return resolveReleasedCompatibilityBaseline(previousMinorLine)
}

func resolveReleasedCompatibilityBaseline(minorLine string) (string, error) {
	releaseRepos := []string{"rancher-latest", "rancher-prime"}
	var bestVersion *goversion.Version

	for _, repoAlias := range releaseRepos {
		results, err := searchHelmRepoVersions(repoAlias)
		if err != nil {
			continue
		}

		versionString, err := findLatestMinorRelease(results, minorLine)
		if err != nil {
			continue
		}

		parsed, err := goversion.NewVersion(versionString)
		if err != nil {
			continue
		}

		if bestVersion == nil || parsed.GreaterThan(bestVersion) {
			bestVersion = parsed
		}
	}

	if bestVersion == nil {
		return "", fmt.Errorf("no released compatibility baseline found for Rancher %s.x", minorLine)
	}

	return bestVersion.Original(), nil
}

func previousRancherMinorLine(minorLine string) (string, error) {
	parts := strings.Split(minorLine, ".")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid Rancher minor line %q", minorLine)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", err
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", err
	}
	if minor == 0 {
		return "", fmt.Errorf("no earlier Rancher minor line exists before %s", minorLine)
	}

	return fmt.Sprintf("%d.%d", major, minor-1), nil
}

func searchHelmRepoVersions(repoAlias string) ([]helmSearchResult, error) {
	chartRef := fmt.Sprintf("%s/rancher", repoAlias)
	output, err := exec.Command("helm", "search", "repo", chartRef, "--devel", "--versions", "-o", "json").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to query helm repo %s: %w", repoAlias, err)
	}

	var results []helmSearchResult
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, fmt.Errorf("failed to parse helm search results for %s: %w", repoAlias, err)
	}
	if len(results) > 0 {
		return results, nil
	}

	globalResults, err := searchAllHelmRepoVersions()
	if err != nil {
		return results, nil
	}

	return filterHelmSearchResultsByRepoAlias(globalResults, repoAlias), nil
}

func searchAllHelmRepoVersions() ([]helmSearchResult, error) {
	output, err := exec.Command("helm", "search", "repo", "--regexp", ".*/rancher$", "--devel", "--versions", "-o", "json").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to query helm repos globally for rancher charts: %w", err)
	}

	var results []helmSearchResult
	if err := json.Unmarshal(output, &results); err != nil {
		return nil, fmt.Errorf("failed to parse global helm search results: %w", err)
	}
	return results, nil
}

func filterHelmSearchResultsByRepoAlias(results []helmSearchResult, repoAlias string) []helmSearchResult {
	prefix := repoAlias + "/"
	filtered := make([]helmSearchResult, 0)
	for _, result := range results {
		if strings.HasPrefix(result.Name, prefix) {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func hasChartVersion(results []helmSearchResult, version string) bool {
	for _, result := range results {
		if result.Version == version {
			return true
		}
	}
	return false
}

func findLatestMinorRelease(results []helmSearchResult, minorLine string) (string, error) {
	var candidates []*goversion.Version
	for _, result := range results {
		if !strings.HasPrefix(result.Version, minorLine+".") || strings.Contains(result.Version, "-") {
			continue
		}
		parsed, err := goversion.NewVersion(result.Version)
		if err == nil {
			candidates = append(candidates, parsed)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no released chart version found for Rancher %s.x", minorLine)
	}

	slices.SortFunc(candidates, func(a, b *goversion.Version) int {
		return b.Compare(a)
	})
	return candidates[0].Original(), nil
}

func resolveImageSettings(requestedVersion, buildType, resolvedDistro string) (string, string, string, []string) {
	switch resolvedDistro {
	case "prime":
		if buildType == "release" {
			return "registry.rancher.com/rancher/rancher", "", "", []string{"Using Rancher Prime registry because distro=prime was requested explicitly"}
		}
		return "registry.rancher.com/rancher/rancher", "v" + requestedVersion, "", []string{"Using Rancher Prime registry because distro=prime was requested explicitly"}
	case "community-staging":
		imageTag := "v" + requestedVersion
		agentImage := fmt.Sprintf("stgregistry.suse.com/rancher/rancher-agent:%s", imageTag)
		return "stgregistry.suse.com/rancher/rancher", imageTag, agentImage, []string{"Using staging Rancher images because the requested version is not a standard released community build"}
	default:
		if buildType == "release" {
			return "", "", "", []string{"Using released community Rancher chart/image defaults"}
		}
		return "", "v" + requestedVersion, "", []string{"Using released community Rancher chart/image settings"}
	}
}

func buildSupportMatrixURL(releasedVersion string) string {
	pathVersion := strings.ReplaceAll(releasedVersion, ".", "-")
	return fmt.Sprintf("https://www.suse.com/suse-rancher/support-matrix/all-supported-versions/rancher-v%s/", pathVersion)
}

func resolveHighestSupportedK3SMinor(supportMatrixURL string) (int, string, error) {
	body, err := fetchURLBody(supportMatrixURL)
	if err != nil {
		return 0, "", err
	}

	textContent, err := extractTextFromHTML(body)
	if err != nil {
		return 0, "", fmt.Errorf("failed to parse support matrix page %s: %w", supportMatrixURL, err)
	}

	patterns := []*regexp.Regexp{
		regexp.MustCompile(`K3s\s+v1\.(\d+)\s+v1\.(\d+)`),
		regexp.MustCompile(`K3s[^\n\r]*?v1\.(\d+)[^\n\r]*?v1\.(\d+)`),
	}

	for _, pattern := range patterns {
		matches := pattern.FindStringSubmatch(textContent)
		if len(matches) == 3 {
			highestMinor, err := strconv.Atoi(matches[2])
			if err != nil {
				return 0, "", fmt.Errorf("failed to parse supported K3s minor %q: %w", matches[2], err)
			}
			return highestMinor, fmt.Sprintf("Support matrix certifies K3s from v1.%s through v1.%s", matches[1], matches[2]), nil
		}
	}

	return 0, "", fmt.Errorf("could not find supported K3s range in %s", supportMatrixURL)
}

func resolveLatestK3SPatch(highestMinor int) (string, error) {
	releaseNotesURL := fmt.Sprintf("https://docs.k3s.io/release-notes/v1.%d.X", highestMinor)
	body, err := fetchURLBody(releaseNotesURL)
	if err != nil {
		return "", err
	}

	pattern := regexp.MustCompile(fmt.Sprintf(`v1\.%d\.\d+\+k3s\d+`, highestMinor))
	matches := pattern.FindAllString(body, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("could not find a K3s patch release in %s", releaseNotesURL)
	}

	var bestVersion *goversion.Version
	var bestOriginal string
	for _, match := range matches {
		normalized := strings.TrimPrefix(strings.Replace(match, "+k3s", "-k3s", 1), "v")
		parsed, err := goversion.NewVersion(normalized)
		if err != nil {
			continue
		}
		if bestVersion == nil || parsed.GreaterThan(bestVersion) {
			bestVersion = parsed
			bestOriginal = match
		}
	}
	if bestOriginal == "" {
		return "", fmt.Errorf("could not parse K3s patch releases in %s", releaseNotesURL)
	}

	return bestOriginal, nil
}

func buildAutoHelmCommands(totalInstances int, chartRepoAlias, chartVersion, bootstrapPassword, rancherImage, rancherImageTag, agentImage string) []string {
	baseSettings := []string{
		"helm install rancher " + chartRepoAlias + "/rancher \\",
		"  --namespace cattle-system \\",
		"  --version " + chartVersion + " \\",
		"  --set hostname=placeholder \\",
		"  --set bootstrapPassword=" + bootstrapPassword + " \\",
		"  --set global.cattle.psp.enabled=false \\",
		"  --set tls=external \\",
		"  --set agentTLSMode=system-store",
	}

	if rancherImage != "" {
		baseSettings = append(baseSettings[:len(baseSettings)-1], append([]string{"  --set rancherImage=" + rancherImage + " \\"}, baseSettings[len(baseSettings)-1:]...)...)
	}
	if rancherImageTag != "" {
		baseSettings = append(baseSettings[:len(baseSettings)-1], append([]string{"  --set rancherImageTag=" + rancherImageTag + " \\"}, baseSettings[len(baseSettings)-1:]...)...)
	}
	if agentImage != "" {
		baseSettings = append(baseSettings[:len(baseSettings)-1], append([]string{
			"  --set 'extraEnv[0].name=CATTLE_AGENT_IMAGE' \\",
			"  --set 'extraEnv[0].value=" + agentImage + "' \\",
		}, baseSettings[len(baseSettings)-1:]...)...)
	}

	command := strings.Join(baseSettings, "\n")
	commands := make([]string, totalInstances)
	for i := 0; i < totalInstances; i++ {
		commands[i] = command
	}
	return commands
}

func buildK3SAirgapImageURL(version string) string {
	return fmt.Sprintf("https://github.com/k3s-io/k3s/releases/download/%s/k3s-airgap-images-amd64.tar.zst", strings.ReplaceAll(version, "+", "%2B"))
}

func buildK3SInstallScriptURL(version string) string {
	return fmt.Sprintf("https://raw.githubusercontent.com/k3s-io/k3s/%s/install.sh", strings.ReplaceAll(version, "+", "%2B"))
}

func fetchURLBody(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected HTTP status %d fetching %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", url, err)
	}
	return string(body), nil
}

func extractTextFromHTML(document string) (string, error) {
	root, err := html.Parse(strings.NewReader(document))
	if err != nil {
		return "", err
	}

	var textParts []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			text := strings.TrimSpace(node.Data)
			if text != "" {
				textParts = append(textParts, text)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)

	return strings.Join(textParts, " "), nil
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
