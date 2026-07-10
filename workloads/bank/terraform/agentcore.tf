# bank-agent: an AI spending-insights assistant hosted on AWS Bedrock
# AgentCore Runtime. Unlike bank-lambda, packaging is a container image (not
# a zip) pushed to ECR out-of-band by scripts/build-bank-agent.sh — AgentCore
# Runtime requires ARM64 images, see bank-agent/Dockerfile.
#
# The ECR repository itself is managed by the separate bootstrap/ root
# module, not here — CreateAgentRuntime needs an image to already exist at
# the given tag, so the repo has to exist (and have an image pushed to it)
# before this module's Agent Runtime can be created. Apply bootstrap/ first,
# run scripts/build-bank-agent.sh, then apply this module — see the README's
# "AWS Bedrock AgentCore (bank-agent)" section for the exact sequence. This
# is an ordering requirement, not an opt-in: bank-agent is a core part of
# the demo, so (like bank-lambda) everything below is unconditional except
# where it's genuinely gated on auth_mode.
#
# These resources (aws_bedrockagentcore_agent_runtime,
# aws_bedrockagentcore_oauth2_credential_provider) were only added to
# terraform-provider-aws in 6.52.0/6.47.0 respectively — both are new enough
# that the exact schema below should be checked with `terraform validate`
# against the registry docs before the first `apply`, rather than trusted
# blindly.

data "aws_ecr_repository" "bank_agent" {
  name = var.bank_agent_ecr_repository_name
}

# scripts/build-bank-agent.sh tags each build with a UTC timestamp
# (YYYYMMDDHHMMSS) rather than a fixed tag like "latest" — a mutable tag
# would mean container_uri below never changes string value between builds,
# so Terraform would see "no changes" and never tell AWS to re-resolve it,
# leaving the Agent Runtime on a stale image (see git history for the
# -replace workaround this replaces). Timestamps sort correctly as strings
# because of the fixed zero-padded width, so the lexically greatest tag is
# the most recently pushed one — no value needs to be copied from the build
# step to this one.
data "aws_ecr_images" "bank_agent" {
  repository_name = var.bank_agent_ecr_repository_name
}

locals {
  bank_agent_image_tags = sort([
    for img in data.aws_ecr_images.bank_agent.image_ids : img.image_tag
    if img.image_tag != null && can(regex("^[0-9]{14}$", img.image_tag))
  ])
  bank_agent_image_tag = local.bank_agent_image_tags[length(local.bank_agent_image_tags) - 1]
}

# --- Execution role ---

resource "aws_iam_role" "bank_agent" {
  name = "${var.bank_agent_ecr_repository_name}-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "bedrock-agentcore.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "bank_agent_ecr_pull" {
  name = "${var.bank_agent_ecr_repository_name}-ecr-pull"
  role = aws_iam_role.bank_agent.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["ecr:GetAuthorizationToken"]
        Resource = "*"
      },
      {
        Effect   = "Allow"
        Action   = ["ecr:BatchGetImage", "ecr:GetDownloadUrlForLayer"]
        Resource = data.aws_ecr_repository.bank_agent.arn
      },
    ]
  })
}

resource "aws_iam_role_policy" "bank_agent_logs" {
  name = "${var.bank_agent_ecr_repository_name}-logs"
  role = aws_iam_role.bank_agent.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"]
      Resource = "*"
    }]
  })
}

resource "aws_iam_role_policy" "bank_agent_bedrock_invoke" {
  name = "${var.bank_agent_ecr_repository_name}-bedrock-invoke"
  role = aws_iam_role.bank_agent.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["bedrock:InvokeModel", "bedrock:InvokeModelWithResponseStream"]
      Resource = "*"
    }]
  })
}

# Only needed when bank-agent exchanges a user's delegated token for a
# downstream credential via AgentCore Identity's On-Behalf-Of exchange
# (agent.py's _exchange_for_delegated_token). Validating and vending the
# *inbound* token (GetWorkloadAccessTokenForJWT) is done by AgentCore
# Runtime itself via its own service-linked role, not this execution role.
resource "aws_iam_role_policy" "bank_agent_obo_exchange" {
  count = var.auth_mode == "spiffe" ? 1 : 0

  name = "${var.bank_agent_ecr_repository_name}-obo-exchange"
  role = aws_iam_role.bank_agent.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["bedrock-agentcore:GetResourceOauth2Token"]
      Resource = "*"
    }]
  })
}

# --- Credex Credential Provider (spiffe mode only) ---
#
# Registers Credex as an OAuth2 Credential Provider so AgentCore Identity's
# On-Behalf-Of token exchange can broker delegated tokens on bank-agent's
# behalf — see workloads/bank/docs/agentcore-identity.md for the mechanics.
#
# Client authentication (client_authentication_method = AWS_IAM_ID_TOKEN_JWT
# below) uses Credex's RFC 7523 jwt-bearer path (exchange/oauth/authserver/
# client_auth.go), which validates the AWS-issued token against a
# trusted-issuer JWKS resolver rather than requiring a SPIFFE SVID — but this
# still requires Credex to have this AWS account's web-identity-federation
# issuer registered as a trusted issuer, which is not yet done (as of this
# writing). The actor token, however, deliberately uses actorTokenContent =
# "M2M" below rather than AWS_IAM_ID_TOKEN_JWT: Credex's actor_token_type
# validation (exchange/oauth/handler/rfc8693/handler.go's
# supportedActorTokenTypes) only accepts "access_token" or "jwt_spiffe" — a
# raw external JWT is not a valid actor token, only a valid client
# assertion/subject token. M2M makes AgentCore fetch a Credex-issued
# client-credentials access token first (authenticating the same jwt-bearer
# way) and present that as the actor token, which lands on the
# "access_token" path.
#
# terraform-provider-aws 6.53.0 (the latest available at the time this was
# written) doesn't yet expose two fields this integration needs —
# client_authentication_method and on_behalf_of_token_exchange_config —
# confirmed by inspecting `terraform providers schema -json`, not assumed.
# The base resource manages what the provider *does* support; the
# null_resource below layers on the missing fields with a direct AWS CLI
# call to the same underlying API, as a stopgap until the provider catches
# up. Re-check the provider's CHANGELOG before removing this workaround.
resource "aws_bedrockagentcore_oauth2_credential_provider" "credex" {
  count = var.auth_mode == "spiffe" ? 1 : 0

  name                       = "credex-provider"
  credential_provider_vendor = "CustomOauth2"

  oauth2_provider_config {
    custom_oauth2_provider_config {
      client_secret = var.credex_client_secret

      oauth_discovery {
        discovery_url = var.credex_discovery_url
      }
    }
  }
}

locals {
  # The fields terraform-provider-aws doesn't yet expose (see comment
  # above), sent via a direct AWS CLI call instead.
  credex_obo_config_json = jsonencode({
    customOauth2ProviderConfig = {
      oauthDiscovery = {
        discoveryUrl = var.credex_discovery_url
      }
      clientSecret               = var.credex_client_secret
      clientAuthenticationMethod = var.credex_client_authentication_method
      onBehalfOfTokenExchangeConfig = {
        grantType = "TOKEN_EXCHANGE"
        tokenExchangeGrantTypeConfig = {
          # Not AWS_IAM_ID_TOKEN_JWT: Credex only accepts "access_token" or
          # "jwt_spiffe" as actor_token_type (see comment above the
          # aws_bedrockagentcore_oauth2_credential_provider resource). M2M
          # has AgentCore fetch a Credex-issued access token via
          # client-credentials first, landing on the "access_token" path.
          actorTokenContent = "M2M"
        }
      }
    }
  })
}

resource "null_resource" "credex_obo_config" {
  count = var.auth_mode == "spiffe" ? 1 : 0

  triggers = {
    discovery_url                = var.credex_discovery_url
    client_authentication_method = var.credex_client_authentication_method
  }

  provisioner "local-exec" {
    command = <<-EOT
      aws bedrock-agentcore-control update-oauth2-credential-provider \
        --name ${aws_bedrockagentcore_oauth2_credential_provider.credex[0].name} \
        --credential-provider-vendor CustomOauth2 \
        --oauth2-provider-config-input ${jsonencode(local.credex_obo_config_json)}
    EOT
  }

  depends_on = [aws_bedrockagentcore_oauth2_credential_provider.credex]
}

# --- Agent Runtime ---

resource "aws_bedrockagentcore_agent_runtime" "bank_agent" {
  agent_runtime_name = replace(var.bank_agent_ecr_repository_name, "-", "_")
  role_arn           = aws_iam_role.bank_agent.arn

  agent_runtime_artifact {
    container_configuration {
      container_uri = "${data.aws_ecr_repository.bank_agent.repository_url}:${local.bank_agent_image_tag}"
    }
  }

  network_configuration {
    network_mode = "PUBLIC"
  }

  # Inbound auth: always the configured OIDC IdP, regardless of auth_mode —
  # signing in as a customer is orthogonal to the static/SPIFFE toggle for
  # the bank-agent -> bank-server hop.
  authorizer_configuration {
    custom_jwt_authorizer {
      discovery_url   = var.oidc_discovery_url
      allowed_clients = var.oidc_allowed_clients
    }
  }

  environment_variables = merge(
    {
      AUTH_MODE               = var.auth_mode
      BANK_SERVER_SUMMARY_URL = var.bank_server_agent_api_url
      BEDROCK_MODEL_ID        = var.bedrock_model_id
    },
    var.auth_mode == "spiffe" ? {
      CREDEX_PROVIDER_NAME = aws_bedrockagentcore_oauth2_credential_provider.credex[0].name
      } : {
      STATIC_AGENT_API_KEY = var.static_agent_api_key
    },
  )
}
