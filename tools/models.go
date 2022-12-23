package toolkit

import "time"

type TokenBody struct {
	Type     string `json:"type"`
	Metadata struct {
	} `json:"metadata"`
	Description string `json:"description"`
	TTL         int    `json:"ttl"`
}

type TokenResponse struct {
	AuthProvider    string      `json:"authProvider"`
	BaseType        string      `json:"baseType"`
	ClusterID       interface{} `json:"clusterId"`
	Created         time.Time   `json:"created"`
	CreatedTS       int64       `json:"createdTS"`
	CreatorID       interface{} `json:"creatorId"`
	Current         bool        `json:"current"`
	Description     string      `json:"description"`
	Enabled         bool        `json:"enabled"`
	Expired         bool        `json:"expired"`
	ExpiresAt       string      `json:"expiresAt"`
	GroupPrincipals interface{} `json:"groupPrincipals"`
	ID              string      `json:"id"`
	IsDerived       bool        `json:"isDerived"`
	Labels          struct {
		AuthnManagementCattleIoTokenUserID string `json:"authn.management.cattle.io/token-userId"`
		CattleIoCreator                    string `json:"cattle.io/creator"`
	} `json:"labels"`
	LastUpdateTime string `json:"lastUpdateTime"`
	Links          struct {
		Remove string `json:"remove"`
		Self   string `json:"self"`
		Update string `json:"update"`
	} `json:"links"`
	Name          string `json:"name"`
	Token         string `json:"token"`
	TTL           int    `json:"ttl"`
	Type          string `json:"type"`
	UserID        string `json:"userId"`
	UserPrincipal string `json:"userPrincipal"`
	UUID          string `json:"uuid"`
}

type LoginPayload struct {
	Description  string `json:"description"`
	ResponseType string `json:"responseType"`
	Username     string `json:"username"`
	Password     string `json:"password"`
}

type LoginResponse struct {
	AuthProvider    string      `json:"authProvider"`
	BaseType        string      `json:"baseType"`
	ClusterID       interface{} `json:"clusterId"`
	Created         time.Time   `json:"created"`
	CreatedTS       int64       `json:"createdTS"`
	CreatorID       interface{} `json:"creatorId"`
	Current         bool        `json:"current"`
	Description     string      `json:"description"`
	Enabled         bool        `json:"enabled"`
	Expired         bool        `json:"expired"`
	ExpiresAt       string      `json:"expiresAt"`
	GroupPrincipals interface{} `json:"groupPrincipals"`
	ID              string      `json:"id"`
	IsDerived       bool        `json:"isDerived"`
	Labels          struct {
		AuthnManagementCattleIoKind        string `json:"authn.management.cattle.io/kind"`
		AuthnManagementCattleIoTokenUserID string `json:"authn.management.cattle.io/token-userId"`
		CattleIoCreator                    string `json:"cattle.io/creator"`
	} `json:"labels"`
	LastUpdateTime string `json:"lastUpdateTime"`
	Links          struct {
		Self string `json:"self"`
	} `json:"links"`
	Name          string `json:"name"`
	Token         string `json:"token"`
	TTL           int    `json:"ttl"`
	Type          string `json:"type"`
	UserID        string `json:"userId"`
	UserPrincipal string `json:"userPrincipal"`
	UUID          string `json:"uuid"`
}

type ImportPayload struct {
	Type     string `json:"type"`
	Metadata struct {
		Namespace string `json:"namespace"`
		Name      string `json:"name"`
	} `json:"metadata"`
	Spec struct {
	} `json:"spec"`
}

type RegistrationResponse struct {
	Type  string `json:"type"`
	Links struct {
		Self string `json:"self"`
	} `json:"links"`
	CreateTypes struct {
		ClusterRegistrationToken string `json:"clusterRegistrationToken"`
	} `json:"createTypes"`
	Actions struct {
	} `json:"actions"`
	Pagination struct {
		Limit int `json:"limit"`
		Total int `json:"total"`
	} `json:"pagination"`
	Sort struct {
		Order   string `json:"order"`
		Reverse string `json:"reverse"`
		Links   struct {
			Command                    string `json:"command"`
			InsecureCommand            string `json:"insecureCommand"`
			InsecureNodeCommand        string `json:"insecureNodeCommand"`
			InsecureWindowsNodeCommand string `json:"insecureWindowsNodeCommand"`
			ManifestURL                string `json:"manifestUrl"`
			NodeCommand                string `json:"nodeCommand"`
			State                      string `json:"state"`
			Token                      string `json:"token"`
			Transitioning              string `json:"transitioning"`
			TransitioningMessage       string `json:"transitioningMessage"`
			UUID                       string `json:"uuid"`
			WindowsNodeCommand         string `json:"windowsNodeCommand"`
		} `json:"links"`
	} `json:"sort"`
	Filters struct {
		ClusterID                  interface{} `json:"clusterId"`
		Command                    interface{} `json:"command"`
		Created                    interface{} `json:"created"`
		CreatorID                  interface{} `json:"creatorId"`
		InsecureCommand            interface{} `json:"insecureCommand"`
		InsecureNodeCommand        interface{} `json:"insecureNodeCommand"`
		InsecureWindowsNodeCommand interface{} `json:"insecureWindowsNodeCommand"`
		ManifestURL                interface{} `json:"manifestUrl"`
		Name                       interface{} `json:"name"`
		NamespaceID                interface{} `json:"namespaceId"`
		NodeCommand                interface{} `json:"nodeCommand"`
		Removed                    interface{} `json:"removed"`
		State                      interface{} `json:"state"`
		Token                      interface{} `json:"token"`
		Transitioning              interface{} `json:"transitioning"`
		TransitioningMessage       interface{} `json:"transitioningMessage"`
		UUID                       interface{} `json:"uuid"`
		WindowsNodeCommand         interface{} `json:"windowsNodeCommand"`
	} `json:"filters"`
	ResourceType string `json:"resourceType"`
	Data         []struct {
		BaseType                   string      `json:"baseType"`
		ClusterID                  string      `json:"clusterId"`
		Command                    string      `json:"command"`
		Created                    time.Time   `json:"created"`
		CreatedTS                  int64       `json:"createdTS"`
		CreatorID                  interface{} `json:"creatorId"`
		ID                         string      `json:"id"`
		InsecureCommand            string      `json:"insecureCommand"`
		InsecureNodeCommand        string      `json:"insecureNodeCommand"`
		InsecureWindowsNodeCommand string      `json:"insecureWindowsNodeCommand"`
		Links                      struct {
			Remove string `json:"remove"`
			Self   string `json:"self"`
			Update string `json:"update"`
		} `json:"links"`
		ManifestURL          string      `json:"manifestUrl"`
		Name                 string      `json:"name"`
		NamespaceID          interface{} `json:"namespaceId"`
		NodeCommand          string      `json:"nodeCommand"`
		State                string      `json:"state"`
		Token                string      `json:"token"`
		Transitioning        string      `json:"transitioning"`
		TransitioningMessage string      `json:"transitioningMessage"`
		Type                 string      `json:"type"`
		UUID                 string      `json:"uuid"`
		WindowsNodeCommand   string      `json:"windowsNodeCommand"`
	} `json:"data"`
}
