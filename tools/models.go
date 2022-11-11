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
