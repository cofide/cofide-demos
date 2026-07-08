# Separate root module, separate state, from the rest of workloads/bank/terraform.
#
# bank-agent's Agent Runtime (in the main module) needs a container image to
# already exist at a given tag before it can be created — CreateAgentRuntime
# fails otherwise. Since the ECR repo has to exist before an image can be
# pushed to it, and the image has to exist before the Agent Runtime can be
# created, the repo can't be managed in the same apply as the Agent Runtime.
# Splitting it into its own module removes the chicken-and-egg problem
# entirely: apply this module, push an image, then apply the main module —
# no `-target` needed. See workloads/bank/README.md's "AWS Bedrock AgentCore
# (bank-agent)" section for the full sequence.
resource "aws_ecr_repository" "bank_agent" {
  name = var.bank_agent_ecr_repository_name
}
