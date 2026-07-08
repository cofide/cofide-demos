output "bank_agent_invoke_url" {
  description = "bank-agent's AgentCore Runtime invoke URL — pass to Helm's bankAgent.invokeUrl."
  value       = "https://bedrock-agentcore.${var.aws_region}.amazonaws.com/runtimes/${urlencode(aws_bedrockagentcore_agent_runtime.bank_agent.agent_runtime_arn)}/invocations?qualifier=DEFAULT"
}
