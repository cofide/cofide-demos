variable "aws_region" {
  description = "The AWS region to create the ECR repository in. Must match the main module's aws_region."
  type        = string
}

variable "bank_agent_ecr_repository_name" {
  description = "Name of the ECR repository bank-agent's container image is pushed to. Must match the main module's bank_agent_ecr_repository_name."
  type        = string
  default     = "cofide-bank-demo-agent"
}

# --- Ory Network OIDC client (bank-client sign-in) ---
#
# Assumes an Ory Network project already exists, with Google already
# configured as a social sign-in provider in it (Ory Console > your project
# > Social Sign-In) — that federation setup isn't managed here, it's an
# existing prerequisite. Signing in during a demo just means using a real
# Google account through Ory's hosted login UI; there's no separate
# Ory-native test identity to create or manage.
#
# The Project API Key isn't a variable here — see versions.tf — export
# ORY_PROJECT_API_KEY instead.

variable "ory_project_slug" {
  description = "Ory Network project slug, e.g. the <slug> in <slug>.projects.oryapis.com. This is the only project identifier this module needs — project_api_key is already project-scoped, and the slug alone is enough to authenticate ory_oauth2_client operations and derive the discovery URL. There's no ory_project_id variable on purpose: the ory_project data source resolves by ID, which would just mean supplying a second identifier to re-derive a slug we already have."
  type        = string
}

variable "ory_custom_domain" {
  description = "Custom domain for the Ory project's public API, if configured (see the ory_custom_domain resource type) — overrides the default <slug>.projects.oryapis.com hostname used to derive the OIDC discovery URL. Leave empty to use the default."
  type        = string
  default     = ""
}

variable "oidc_redirect_url" {
  description = "OAuth2 redirect URL bank-client's Ory client is registered with, e.g. https://<dashboard-host>/callback. Must match bank-client's own OIDC_REDIRECT_URL — depends on your ingress/DNS, so there's no default."
  type        = string
}
