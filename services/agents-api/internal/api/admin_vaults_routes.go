package api

import "net/http"

// @Summary List Vaults in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Vaults
// @Produce json
// @Security DeploymentAdminAuth
// @Param after query string false "Last Vault ID from the previous page"
// @Param limit query integer false "Requested page size, clamped to 1–100" default(20)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Param status query string false "Scalar status filter" Enums(active,archived)
// @Param status[] query []string false "Array status filter; combined with status as a union" collectionFormat(multi) Enums(active,archived)
// @Success 200 {object} v1.VaultList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/vaults [get]
func (h *Handler) adminListVaults(w http.ResponseWriter, r *http.Request) {
	h.listVaults(w, r)
}

// @Summary Retrieve a Vault in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Vaults
// @Produce json
// @Security DeploymentAdminAuth
// @Param vault_id path string true "Vault ID"
// @Success 200 {object} v1.Vault
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/vaults/{vault_id} [get]
func (h *Handler) adminGetVault(w http.ResponseWriter, r *http.Request) {
	h.getVault(w, r)
}

// @Summary Delete a Vault and all its Credentials in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Vaults
// @Produce json
// @Security DeploymentAdminAuth
// @Param vault_id path string true "Vault ID"
// @Success 200 {object} v1.VaultDeleted
// @Failure 400,401,404,413,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/vaults/{vault_id} [delete]
func (h *Handler) adminDeleteVault(w http.ResponseWriter, r *http.Request) {
	h.deleteVault(w, r)
}

// @Summary List safe Vault Credential metadata in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Credentials
// @Produce json
// @Security DeploymentAdminAuth
// @Param vault_id path string true "Vault ID"
// @Param after query string false "Last Credential ID from the previous page"
// @Param limit query integer false "Requested page size, clamped to 1–100" default(20)
// @Param order query string false "Creation order; omit for descending, explicit empty values are invalid" Enums(asc,desc) default(desc)
// @Param status query string false "Scalar status filter" Enums(active,archived)
// @Param status[] query []string false "Array status filter; combined with status as a union" collectionFormat(multi) Enums(active,archived)
// @Success 200 {object} v1.CredentialList
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/vaults/{vault_id}/credentials [get]
func (h *Handler) adminListCredentials(w http.ResponseWriter, r *http.Request) {
	h.listCredentials(w, r)
}

// @Summary Retrieve safe Vault Credential metadata in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Credentials
// @Produce json
// @Security DeploymentAdminAuth
// @Param vault_id path string true "Vault ID"
// @Param credential_id path string true "Credential ID"
// @Success 200 {object} v1.Credential
// @Failure 400,401,404,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/vaults/{vault_id}/credentials/{credential_id} [get]
func (h *Handler) adminGetCredential(w http.ResponseWriter, r *http.Request) {
	h.getCredential(w, r)
}

// @Summary Delete a Vault Credential in a managed key space
// @Description Deployment administrator only. Reuses the public resource projection and operation rules; the key selects the target space and does not authenticate.
// @Tags Credentials
// @Produce json
// @Security DeploymentAdminAuth
// @Param vault_id path string true "Vault ID"
// @Param credential_id path string true "Credential ID"
// @Success 200 {object} v1.CredentialDeleted
// @Failure 400,401,404,413,500 {object} v1.ErrorResponse
// @Param project_id path string true "Project ID"
// @Router /core/v1/admin/projects/{project_id}/vaults/{vault_id}/credentials/{credential_id} [delete]
func (h *Handler) adminDeleteCredential(w http.ResponseWriter, r *http.Request) {
	h.deleteCredential(w, r)
}
