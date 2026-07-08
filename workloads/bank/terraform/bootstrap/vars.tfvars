aws_region = "eu-west-1"

# Uncomment to override the default repository name (must match the main
# module's bank_agent_ecr_repository_name).
# bank_agent_ecr_repository_name = "cofide-bank-demo-agent"

# Fill these in with your actual Ory Network project slug and bank-client's
# real callback URL before applying.
ory_project_slug  = "condescending-kare-i02usaqmz7"
oidc_redirect_url = "http://localhost:8081/callback"

# Also required, but not set here or in any tfvars file (see versions.tf
# and variables.tf for why): export it before running terraform.
#   export ORY_PROJECT_API_KEY=ory_pat_...
