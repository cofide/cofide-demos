variable "aws_region" {
  description = "The AWS region to deploy the Lambda function into."
  type        = string
}

variable "function_name" {
  description = "Name of the Lambda function."
  type        = string
  default     = "cofide-bank-demo-lambda"
}

variable "auth_mode" {
  description = "Auth mode for calls to bank-server: \"static\" (pre-shared API key) or \"spiffe\" (JWT-SVID minted by Cofide Credex)."
  type        = string

  validation {
    condition     = contains(["static", "spiffe"], var.auth_mode)
    error_message = "auth_mode must be either \"static\" or \"spiffe\"."
  }
}

variable "bank_server_webhook_url" {
  description = "URL of bank-server's webhook endpoint (e.g. http://<external-address>:8444/webhook/transactions), reachable from AWS."
  type        = string
}

variable "static_webhook_api_key" {
  description = "Pre-shared API key sent as a bearer token when auth_mode = \"static\". Must match bank-server's STATIC_WEBHOOK_API_KEY."
  type        = string
  default     = ""
  sensitive   = true
}

variable "token_exchange_url" {
  description = "URL of the Cofide Credex token exchange endpoint, used when auth_mode = \"spiffe\"."
  type        = string
  default     = ""
}

# --- bank-agent (AWS Bedrock AgentCore) ---
#
# bank-agent's AWS resources (execution role, AgentCore Runtime, and, when
# auth_mode = "spiffe", the Credex Credential Provider) are unconditional,
# just like bank-lambda's — it's a core part of the demo, not an optional
# add-on. The one real constraint is ordering, not opt-in: the ECR repo
# (terraform/bootstrap, a separate root module) must exist and have an image
# pushed to it (scripts/build-bank-agent.sh) before this module's Agent
# Runtime can be created — see the README's "AWS Bedrock AgentCore
# (bank-agent)" section for the exact sequence.

variable "bank_agent_ecr_repository_name" {
  description = "Name of the ECR repository bank-agent's container image is pushed to."
  type        = string
  default     = "cofide-bank-demo-agent"
}

variable "bank_server_agent_api_url" {
  description = "URL of bank-server's agent-facing summary endpoint (e.g. http://<external-address>:8445/api/summary), reachable from AWS."
  type        = string
}

variable "bedrock_model_id" {
  description = "Bedrock model ID (or cross-region inference profile) bank-agent uses to answer questions. Defaults to a first-party Amazon model — third-party (e.g. Anthropic) models are AWS Marketplace listings that require a marketplace subscription (aws-marketplace:ViewSubscriptions/Subscribe on the execution role) and, for Anthropic specifically, a one-time account-level use-case submission shared with Anthropic."
  type        = string
  default     = "eu.amazon.nova-lite-v1:0"
}

variable "static_agent_api_key" {
  description = "Pre-shared API key sent as a bearer token by bank-agent when auth_mode = \"static\". Must match bank-server's STATIC_AGENT_API_KEY."
  type        = string
  default     = ""
  sensitive   = true
}

variable "oidc_discovery_url" {
  description = "OIDC discovery URL (any compliant IdP — Ory, Auth0, Okta, ...), used by bank-agent's AgentCore Runtime to validate inbound customer bearer tokens. Required in both auth modes — signing in is orthogonal to the static/SPIFFE toggle."
  type        = string
}

variable "oidc_allowed_clients" {
  description = "OAuth2 client IDs accepted on inbound tokens presented to bank-agent."
  type        = list(string)
}

variable "credex_discovery_url" {
  description = "Cofide Credex OIDC discovery URL, used to configure bank-agent's On-Behalf-Of token exchange Credential Provider when auth_mode = \"spiffe\". Distinct from token_exchange_url: this must be the standards-compliant, OIDC-discoverable endpoint (the shape workloads/ping-pong-exchange uses), not bank-lambda's bespoke JSON endpoint — see workloads/bank/docs/agentcore-identity.md."
  type        = string
  default     = ""
}

variable "credex_client_id" {
  description = "OAuth2 client ID identifying bank-agent to Credex, regardless of credex_client_authentication_method."
  type        = string
  default     = ""
}

variable "credex_client_secret" {
  description = "OAuth2 client secret bank-agent's Credential Provider uses to authenticate to Credex. Only used when credex_client_authentication_method = \"CLIENT_SECRET_BASIC\" or \"CLIENT_SECRET_POST\"."
  type        = string
  default     = ""
  sensitive   = true
}

variable "credex_client_authentication_method" {
  description = "How bank-agent authenticates itself as an OAuth2 client to Credex's token endpoint. \"AWS_IAM_ID_TOKEN_JWT\" (the default) uses an AWS-issued identity token in place of a shared client secret — the more thoroughly AWS-native option, but not yet confirmed as supported by Credex; fall back to \"CLIENT_SECRET_BASIC\" if it isn't. See workloads/bank/docs/agentcore-identity.md."
  type        = string
  default     = "AWS_IAM_ID_TOKEN_JWT"

  validation {
    condition     = contains(["AWS_IAM_ID_TOKEN_JWT", "CLIENT_SECRET_BASIC", "CLIENT_SECRET_POST"], var.credex_client_authentication_method)
    error_message = "credex_client_authentication_method must be one of AWS_IAM_ID_TOKEN_JWT, CLIENT_SECRET_BASIC, CLIENT_SECRET_POST."
  }
}

variable "credex_audience" {
  description = "Audience claim requested on the AWS web identity token presented to Credex."
  type        = string
  default     = "cofide-credex"
}
