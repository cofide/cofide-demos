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

# GetResourceOauth2Token itself calls sts:GetWebIdentityToken under the hood,
# using this execution role, to generate the AWS_IAM_ID_TOKEN_JWT assertion it
# presents to Credex as client authentication — mirrors
# bank_lambda_web_identity_token in iam.tf, which grants the same action for
# bank-lambda's own (hand-rolled) exchange.
resource "aws_iam_role_policy" "bank_agent_web_identity_token" {
  count = var.auth_mode == "spiffe" ? 1 : 0

  name = "${var.bank_agent_ecr_repository_name}-sts-web-identity-token"
  role = aws_iam_role.bank_agent.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["sts:GetWebIdentityToken"]
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
# Client authentication is fixed to AWS_IAM_ID_TOKEN_JWT (not a variable —
# CLIENT_SECRET_BASIC/CLIENT_SECRET_POST support was removed once this path
# was confirmed to work; see git history if that ever needs resurrecting).
# It uses Credex's RFC 7523 jwt-bearer path (exchange/oauth/authserver/
# client_auth.go), which validates the AWS-issued token against a
# trusted-issuer JWKS resolver rather than requiring a SPIFFE SVID — this
# requires Credex to have this AWS account's web-identity-federation issuer
# registered as a trusted issuer. The actor token uses actorTokenContent =
# "AWS_IAM_ID_TOKEN_JWT" below: AgentCore presents its AWS-minted assertion
# directly as actor_token (actor_token_type=urn:ietf:params:oauth:token-type:jwt),
# with no separate client_credentials round trip first. This requires Credex's
# actor_token_type validation (exchange/oauth/handler/rfc8693/handler.go's
# supportedActorTokenTypes) to accept "jwt" as an actor token type, via the
# same trusted-issuer mechanism used for subject tokens and client auth — not
# supported on Credex's main branch as of this writing; requires the
# "external JWT actor tokens" feature (see Credex's own git history/PRs).
# The alternative, actorTokenContent = "M2M" (AgentCore first does a
# client_credentials grant to get a Credex-issued access token, then presents
# that as the actor token, landing on Credex's "access_token" actor path
# instead), avoids that dependency at the cost of a second round trip and a
# second exchange policy — see git history if reverting to that is ever
# needed.
#
# terraform-provider-aws 6.53.0 (the latest available at the time this was
# written) doesn't expose client_authentication_method or
# on_behalf_of_token_exchange_config on custom_oauth2_provider_config —
# confirmed by inspecting `terraform providers schema -json`, not assumed.
# Managed entirely via a direct AWS CLI call (create-or-update) rather than
# the aws_bedrockagentcore_oauth2_credential_provider resource: that
# resource's CreateOauth2CredentialProvider call never sets
# clientAuthenticationMethod (the field it doesn't expose), so AWS defaults
# it to CLIENT_SECRET_BASIC — which then requires a client_id we
# deliberately don't have, and fails at creation with "clientId is required
# for CLIENT_SECRET_BASIC and CLIENT_SECRET_POST authentication methods."
# The underlying CreateOauth2CredentialProvider/UpdateOauth2CredentialProvider
# API does support clientAuthenticationMethod (and
# onBehalfOfTokenExchangeConfig) directly — see AWS's
# CustomOauth2ProviderConfigInput reference — so once the Terraform
# resource's schema catches up, this can become a normal resource again.
# Re-check the provider's CHANGELOG before removing this workaround.
locals {
  credex_provider_name = "credex-provider"

  # AWS's Credential Provider requires an authorization_endpoint, but Credex
  # (a pure RFC 8693 token-exchange service, not a full OAuth2 AS) doesn't
  # have one — using oauthDiscovery.discoveryUrl against Credex's actual
  # discovery document (which omits authorization_endpoint entirely) fails
  # with "Credential Provider with no Authorization Endpoint information".
  # authorizationServerMetadata lets us supply the endpoints directly instead
  # of relying on discovery; authorizationEndpoint is set to Credex's token
  # endpoint since there's no separate one and this OBO flow never actually
  # redirects a user there.
  credex_issuer         = trimsuffix(var.credex_discovery_url, "/.well-known/openid-configuration")
  credex_token_endpoint = "${local.credex_issuer}/token"

  credex_obo_config_json = jsonencode({
    customOauth2ProviderConfig = {
      oauthDiscovery = {
        authorizationServerMetadata = {
          issuer                = local.credex_issuer
          authorizationEndpoint = local.credex_token_endpoint
          tokenEndpoint         = local.credex_token_endpoint
        }
      }
      clientAuthenticationMethod = "AWS_IAM_ID_TOKEN_JWT"
      onBehalfOfTokenExchangeConfig = {
        grantType = "TOKEN_EXCHANGE"
        tokenExchangeGrantTypeConfig = {
          # AgentCore presents its AWS-minted assertion directly as the RFC
          # 8693 actor_token (see comment above) — a single Credex round trip,
          # not the two-call M2M pattern. Requires Credex to accept "jwt" as
          # an actor_token_type (see comment above).
          actorTokenContent = "AWS_IAM_ID_TOKEN_JWT"
        }
      }
    }
  })
}

resource "null_resource" "credex_obo_config" {
  count = var.auth_mode == "spiffe" ? 1 : 0

  triggers = {
    # Hash the whole payload, not just var.credex_discovery_url: the payload's
    # *structure* (e.g. discoveryUrl vs. authorizationServerMetadata) can
    # change without that variable changing, and triggers are the only thing
    # that makes Terraform re-run local-exec — otherwise "terraform apply"
    # reports "No changes" and the stale config silently stays live in AWS.
    config_hash = sha256(local.credex_obo_config_json)
    # Destroy-time provisioners may only reference self.* (Terraform can't
    # guarantee other resources/locals still exist at that point), so the
    # provider name has to be captured here too, not just read from
    # local.credex_provider_name directly in the destroy provisioner below.
    provider_name = local.credex_provider_name
  }

  # create-or-update, not just update: this resource is the sole owner of
  # the Credex credential provider's lifecycle now (see comment above), so
  # the first apply must create it, and only later applies (where triggers
  # changed, destroying and recreating this null_resource) should update an
  # already-existing one.
  provisioner "local-exec" {
    # local-exec runs via /bin/sh (not necessarily bash — e.g. dash on
    # Debian/Ubuntu), so this can't rely on bash-only options like
    # `pipefail`. Not needed anyway: nothing here pipes commands together.
    command = <<-EOT
      set -eu
      if aws bedrock-agentcore-control get-oauth2-credential-provider --name ${local.credex_provider_name} >/dev/null 2>&1; then
        aws bedrock-agentcore-control update-oauth2-credential-provider \
          --name ${local.credex_provider_name} \
          --credential-provider-vendor CustomOauth2 \
          --oauth2-provider-config-input ${jsonencode(local.credex_obo_config_json)}
      else
        aws bedrock-agentcore-control create-oauth2-credential-provider \
          --name ${local.credex_provider_name} \
          --credential-provider-vendor CustomOauth2 \
          --oauth2-provider-config-input ${jsonencode(local.credex_obo_config_json)}
      fi
    EOT
  }

  # Runs whenever this null_resource is destroyed — auth_mode flipping back
  # to "static" (count 1 -> 0), a real `terraform destroy`, or this resource
  # being replaced because triggers changed (destroy-then-recreate) — so the
  # AWS-side credential provider doesn't outlive the Terraform resource that
  # (as of the comment above) is meant to be its sole owner. Tolerates the
  # provider already being gone (e.g. it was never successfully created, or
  # was already cleaned up), so a redundant/failed cleanup never blocks the
  # rest of the apply/destroy from completing.
  provisioner "local-exec" {
    when    = destroy
    command = "aws bedrock-agentcore-control delete-oauth2-credential-provider --name ${self.triggers.provider_name} || true"
  }
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
      discovery_url = var.oidc_discovery_url
      # allowed_audience (checks the JWT's "aud" claim), not allowed_clients
      # (checks a literal "client_id" claim): bank-client forwards its OIDC ID
      # token, not its access token, as the bearer credential (see
      # bank-client/session.go) — ID tokens always set "aud" to the client ID
      # per the OIDC spec, but aren't guaranteed to carry a separate
      # "client_id" claim, which is an access-token convention.
      allowed_audience = var.oidc_allowed_clients
    }
  }

  # Configuring custom_jwt_authorizer above only validates the Authorization
  # header — it does NOT forward it to context.request_headers in agent.py.
  # That's a separate, required opt-in (confirmed via AWS's "Pass custom
  # headers to Amazon Bedrock AgentCore Runtime" doc): without this,
  # agent.py sees only a small set of always-forwarded headers (e.g.
  # baggage, workloadAccessToken) and on_behalf_of is always "unknown".
  request_header_configuration {
    request_header_allowlist = ["Authorization"]
  }

  environment_variables = merge(
    {
      AUTH_MODE               = var.auth_mode
      BANK_SERVER_SUMMARY_URL = var.bank_server_agent_api_url
      BEDROCK_MODEL_ID        = var.bedrock_model_id
    },
    var.auth_mode == "spiffe" ? {
      CREDEX_PROVIDER_NAME = local.credex_provider_name
      CREDEX_OBO_SCOPES    = join(",", var.credex_obo_scopes)
      } : {
      STATIC_AGENT_API_KEY = var.static_agent_api_key
    },
  )

  # CREDEX_PROVIDER_NAME above is a plain string literal (local.credex_provider_name),
  # not a resource attribute reference, so Terraform can't infer that the
  # Credex credential provider must exist before this Runtime references its
  # name — depend on it explicitly instead.
  depends_on = [null_resource.credex_obo_config]
}
