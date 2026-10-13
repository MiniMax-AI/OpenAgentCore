package v1

import "encoding/json"

func (a CredentialAuth) MarshalJSON() ([]byte, error) {
	if a.Type == "static_bearer" {
		return json.Marshal(struct {
			Type         string `json:"type"`
			MCPServerURL string `json:"mcp_server_url"`
		}{a.Type, a.MCPServerURL})
	}
	type resource CredentialAuth
	return json.Marshal(resource(a))
}
