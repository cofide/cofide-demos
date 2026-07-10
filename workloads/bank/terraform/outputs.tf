output "bank_agent_invoke_url" {
  description = "bank-agent's AgentCore Runtime invoke URL — pass to Helm's bankAgent.invokeUrl."
  value       = "https://bedrock-agentcore.${var.aws_region}.amazonaws.com/runtimes/${urlencode(aws_bedrockagentcore_agent_runtime.bank_agent.agent_runtime_arn)}/invocations?qualifier=DEFAULT"
}

output "bank_agent_execution_role_arn" {
  description = "ARN of bank-agent's IAM execution role — this is the value AWS puts in the \"sub\" claim of the identity JWT bank-agent presents to Credex (see AWS's outbound web identity federation docs), so it's what ends up in a delegated token's \"act.sub\" and what bank-server must be configured to authorize via AGENT_AUTHORIZED_ACTOR."
  value       = aws_iam_role.bank_agent.arn
}
