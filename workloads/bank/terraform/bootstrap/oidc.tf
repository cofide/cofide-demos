# Registers bank-client as a public OAuth2 client (PKCE, no secret — see
# bank-client/login.go) directly in Ory Network, instead of that
# registration being a manual, undocumented step done in the Ory Console.
# Login itself goes through Ory's hosted UI, already federated to Google —
# there's no separate Ory-native identity provisioned here.

resource "ory_oauth2_client" "bank_client" {
  client_name                = "bank-client"
  redirect_uris              = [var.oidc_redirect_url]
  grant_types                = ["authorization_code", "refresh_token"]
  response_types             = ["code"]
  scope                      = "openid offline_access"
  token_endpoint_auth_method = "none"
}

locals {
  # Derived directly from var.ory_project_slug, not looked up via the
  # ory_project data source — that data source resolves by project ID (a
  # separate, workspace-scoped identifier this module has no other use for),
  # which would mean requiring both project_id and project_slug just to
  # re-derive the slug we already have.
  oidc_discovery_url = var.ory_custom_domain != "" ? "https://${var.ory_custom_domain}/.well-known/openid-configuration" : "https://${var.ory_project_slug}.projects.oryapis.com/.well-known/openid-configuration"
}
