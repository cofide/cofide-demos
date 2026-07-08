output "repository_url" {
  description = "ECR repository URL — pass to scripts/build-bank-agent.sh (or let it auto-detect via the AWS CLI)."
  value       = aws_ecr_repository.bank_agent.repository_url
}

output "oidc_client_id" {
  description = "bank-client's Ory OAuth2 client ID — auto-detected by deploy-static.sh/toggle-spiffe.sh."
  value       = ory_oauth2_client.bank_client.client_id
}

output "oidc_discovery_url" {
  description = "Ory project's OIDC discovery URL — auto-detected by deploy-static.sh/toggle-spiffe.sh."
  value       = local.oidc_discovery_url
}

output "oidc_redirect_url" {
  description = "The redirect URL bank-client's Ory client was registered with — auto-detected by deploy-static.sh so it can't drift from what's actually registered."
  value       = var.oidc_redirect_url
}
