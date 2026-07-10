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
import logging
import os

import boto3
import requests
from bedrock_agentcore import BedrockAgentCoreApp
from strands import Agent, tool
from strands.models import BedrockModel

# logging.basicConfig() is a documented no-op if the root logger already has
# handlers attached — which the AgentCore runtime (or one of boto3/
# bedrock_agentcore's own imports, above) does before this module finishes
# loading. Configuring this logger directly, rather than relying on
# basicConfig succeeding, means these log lines show up regardless of
# whatever the runtime already set up on root.
logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)
if not logger.handlers:
    _handler = logging.StreamHandler()
    _handler.setFormatter(logging.Formatter("%(levelname)s %(name)s: %(message)s"))
    logger.addHandler(_handler)

AUTH_MODE = os.environ.get("AUTH_MODE", "static")
BANK_SERVER_SUMMARY_URL = os.environ["BANK_SERVER_SUMMARY_URL"]
BEDROCK_MODEL_ID = os.environ.get("BEDROCK_MODEL_ID", "eu.amazon.nova-lite-v1:0")
CREDEX_PROVIDER_NAME = os.environ.get("CREDEX_PROVIDER_NAME", "credex-provider")
# GetResourceOauth2Token requires a non-empty "scopes" list. bank-server's
# own delegated-token validation (bank-server/delegated_auth.go) doesn't
# check scope at all, so the only thing that actually depends on this is
# whatever Credex's own policy for this exchange expects — matches
# terraform/variables.tf's credex_obo_scopes default; adjust both if that
# policy expects something else.
CREDEX_OBO_SCOPES = [s for s in os.environ.get("CREDEX_OBO_SCOPES", "summary:read").split(",") if s]

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
            logger.info(
                "Calling bank-server auth_method=static-secret caller=bank-agent on_behalf_of_asserted_unverified=%s",
                on_behalf_of,
            )
        elif AUTH_MODE == "spiffe":
            headers["Authorization"] = f"Bearer {_exchange_for_delegated_token(workload_access_token)}"
            logger.info(
                "Calling bank-server auth_method=delegated-jwt caller=bank-agent on_behalf_of_verified=%s",
                on_behalf_of,
            )
        else:
            raise ValueError(f"invalid AUTH_MODE: {AUTH_MODE}")

        try:
            resp = requests.get(BANK_SERVER_SUMMARY_URL, headers=headers, timeout=10)
        except requests.exceptions.RequestException:
            logger.exception("Failed to reach bank-server")
            raise
        if not resp.ok:
            logger.warning("bank-server rejected request status=%s body=%s", resp.status_code, resp.text)
        resp.raise_for_status()
        return resp.text

    return Agent(model=bedrock_model, tools=[get_account_summary], system_prompt=SYSTEM_PROMPT)


def _exchange_for_delegated_token(workload_access_token: str) -> str:
    # Strands' tool-calling loop catches exceptions raised here and has the
    # model narrate a generic apology instead of surfacing them — without
    # this log, a failure in the OBO exchange itself leaves no trace
    # anywhere (bedrock_agentcore.app only logs a rich traceback for
    # exceptions that escape the entrypoint entirely, which a caught tool
    # error never does).
    try:
        resp = identity_client.get_resource_oauth2_token(
            resourceCredentialProviderName=CREDEX_PROVIDER_NAME,
            oauth2Flow="ON_BEHALF_OF_TOKEN_EXCHANGE",
            workloadIdentityToken=workload_access_token,
            scopes=CREDEX_OBO_SCOPES,
        )
    except Exception:
        logger.exception("Credex On-Behalf-Of token exchange failed")
        raise
    logger.info("Credex minted a delegated token via AgentCore Identity's On-Behalf-Of exchange")
    return resp["accessToken"]


def _decode_claim(token: str, claim: str) -> str:
    payload = json.loads(base64.urlsafe_b64decode(token.split(".")[1] + "=="))
    return payload.get(claim, "unknown")


def _get_header(headers: dict, name: str) -> str:
    """Case-insensitive header lookup. context.request_headers' key casing
    isn't guaranteed to match the literal name callers expect — e.g. an
    HTTP/2 connection lowercases all header names on the wire (RFC 7540
    8.1.2), so "Authorization" can arrive as "authorization". A plain dict
    lookup on the exact expected casing would silently miss it.
    """
    name_lower = name.lower()
    for key, value in headers.items():
        if key.lower() == name_lower:
            return value
    return ""


@app.entrypoint
def invoke(payload, context):
    question = payload.get("question", "")

    auth_header = _get_header(context.request_headers, "Authorization")
    user_token = auth_header[len("Bearer ") :] if auth_header.startswith("Bearer ") else ""
    on_behalf_of = _decode_claim(user_token, "sub") if user_token else "unknown"
    workload_access_token = _get_header(context.request_headers, "WorkloadAccessToken")

    if user_token:
        # Credex's subject_token audience check has repeatedly been guessed at
        # (Credex's own token URL, then bank-client's OIDC client ID) without
        # matching — logging the token's actual "aud" claim here settles what
        # Credex must be configured to accept, instead of guessing again.
        logger.info("Inbound user token aud=%s iss=%s", _decode_claim(user_token, "aud"), _decode_claim(user_token, "iss"))

    if on_behalf_of == "unknown":
        # AgentCore Runtime's inbound authorizer already rejects requests
        # without a valid token before this code runs, so reaching here with
        # no usable Authorization header means something about how it's
        # being read is wrong, not that the caller is unauthenticated — log
        # the actual header names received to make that debuggable.
        logger.warning("No usable Authorization header found; received header names: %s", list(context.request_headers.keys()))

    logger.info(
        "Handling request on_behalf_of=%s (verified by AgentCore Runtime's inbound JWT authorizer before this code ran)",
        on_behalf_of,
    )

    agent = build_agent(on_behalf_of, workload_access_token)
    result = agent(question)
    return {"answer": str(result), "onBehalfOf": on_behalf_of}


if __name__ == "__main__":
    app.run()
