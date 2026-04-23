package test

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

type resolvedChartMatch struct {
	repoAlias             string
	chartVersion          string
	compatibilityBaseline string
	matchRank             int
}
