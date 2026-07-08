"""AWS Bedrock AgentCore entrypoint for the Cofide bank spending-insights agent.

Answers a signed-in customer's question about their account by calling
bank-server's /api/summary endpoint. The customer's identity arrives as an
OIDC-issued bearer token (any compliant IdP — Ory, Auth0, Okta, ...),
validated by AgentCore Runtime's inbound custom JWT authorizer before this
code ever runs (see terraform/agentcore.tf) — the signature check below is
skipped deliberately, matching AWS's own documented pattern for reading
claims post-authorization.

Auth mode for the outbound call to bank-server is controlled by AUTH_MODE:

  - "static": sends a pre-shared API key (STATIC_AGENT_API_KEY) as a bearer
    token, plus the caller's identity as an asserted (unverified)
    X-On-Behalf-Of header — the "before Cofide Connect" story.
  - "spiffe": exchanges the inbound user token for a Credex-minted,
    user-as-subject/agent-as-actor delegated token via AgentCore Identity's
    native On-Behalf-Of token exchange, and sends that as the bearer token
    instead. Unlike bank-lambda's handler.py, there is no hand-written
    sts:GetWebIdentityToken or Credex HTTP call here — AgentCore performs
    both against the Credex OAuth2 Credential Provider configured in
    terraform/agentcore.tf.
"""

import base64
import json
import os

import boto3
import requests
from bedrock_agentcore import BedrockAgentCoreApp
from strands import Agent, tool
from strands.models import BedrockModel

AUTH_MODE = os.environ.get("AUTH_MODE", "static")
BANK_SERVER_SUMMARY_URL = os.environ["BANK_SERVER_SUMMARY_URL"]
BEDROCK_MODEL_ID = os.environ.get("BEDROCK_MODEL_ID", "eu.amazon.nova-lite-v1:0")
CREDEX_PROVIDER_NAME = os.environ.get("CREDEX_PROVIDER_NAME", "credex-provider")

SYSTEM_PROMPT = (
    "You are a spending-insights assistant for Cofide Bank. Use the "
    "get_account_summary tool to answer the customer's question about their "
    "balance or recent transactions. Only state facts present in the tool's "
    "response, and keep answers to a couple of sentences. If the question "
    "refers to a grouping that isn't a literal category in the data (e.g. "
    "'non-essentials' or 'subscriptions'), use your own judgement to decide "
    "which categories qualify and briefly say which ones you included."
)

# Bare model-ID strings fall back to Bedrock's default max output tokens,
# which isn't enough headroom for both a visible <thinking> narration and a
# final answer on questions that need the model to reason across multiple
# categories (e.g. "non-essentials") rather than read a single field.
bedrock_model = BedrockModel(model_id=BEDROCK_MODEL_ID, max_tokens=4096)

app = BedrockAgentCoreApp()
identity_client = boto3.client("bedrock-agentcore") if AUTH_MODE == "spiffe" else None


def build_agent(on_behalf_of: str, workload_access_token: str) -> Agent:
    """Build an Agent instance scoped to a single request, with the tool
    closed over that request's caller identity — the tool itself takes no
    arguments, since the model has no legitimate way to supply or override
    who it's acting on behalf of.
    """

    @tool
    def get_account_summary() -> str:
        """Fetch the signed-in customer's account balance and recent transactions from bank-server."""
        headers = {}
        if AUTH_MODE == "static":
            headers["Authorization"] = f"Bearer {os.environ['STATIC_AGENT_API_KEY']}"
            headers["X-On-Behalf-Of"] = on_behalf_of
        elif AUTH_MODE == "spiffe":
            headers["Authorization"] = f"Bearer {_exchange_for_delegated_token(workload_access_token)}"
        else:
            raise ValueError(f"invalid AUTH_MODE: {AUTH_MODE}")

        resp = requests.get(BANK_SERVER_SUMMARY_URL, headers=headers, timeout=10)
        resp.raise_for_status()
        return resp.text

    return Agent(model=bedrock_model, tools=[get_account_summary], system_prompt=SYSTEM_PROMPT)


def _exchange_for_delegated_token(workload_access_token: str) -> str:
    resp = identity_client.get_resource_oauth2_token(
        resourceCredentialProviderName=CREDEX_PROVIDER_NAME,
        oauth2Flow="ON_BEHALF_OF_TOKEN_EXCHANGE",
        workloadIdentityToken=workload_access_token,
    )
    return resp["accessToken"]


def _decode_claim(token: str, claim: str) -> str:
    payload = json.loads(base64.urlsafe_b64decode(token.split(".")[1] + "=="))
    return payload.get(claim, "unknown")


@app.entrypoint
def invoke(payload, context):
    question = payload.get("question", "")

    auth_header = context.request_headers.get("Authorization", "")
    user_token = auth_header[len("Bearer ") :] if auth_header.startswith("Bearer ") else ""
    on_behalf_of = _decode_claim(user_token, "sub") if user_token else "unknown"
    workload_access_token = context.request_headers.get("WorkloadAccessToken", "")

    agent = build_agent(on_behalf_of, workload_access_token)
    result = agent(question)
    return {"answer": str(result), "onBehalfOf": on_behalf_of}


if __name__ == "__main__":
    app.run()
