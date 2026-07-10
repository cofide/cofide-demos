"""AWS Lambda handler for the Cofide bank demo.

Simulates a payments network posting a new transaction to bank-server's
webhook. Auth mode is controlled by AUTH_MODE:

  - "static": sends a pre-shared API key (STATIC_WEBHOOK_API_KEY) as a bearer
    token — the "before Cofide Connect" story.
  - "spiffe": exchanges the Lambda's own AWS identity for a JWT-SVID via
    Cofide Credex, and sends that as the bearer token instead.

Invoke manually during a demo, e.g.:

    aws lambda invoke --function-name cofide-bank-demo-lambda \\
      --payload '{"merchant": "Rail Delivery Group", "category": "Transport", "amountPence": -3450}' \\
      --cli-binary-format raw-in-base64-out out.json
"""

import base64
import json
import logging
import os
import urllib.error
import urllib.request

# logging.basicConfig() is a documented no-op if the root logger already has
# handlers attached — which the Lambda Python runtime does before user code
# runs (AWS's own docs note this and warn against relying on basicConfig
# here). Configuring this logger directly, rather than relying on
# basicConfig succeeding, means these log lines show up regardless of
# whatever the runtime already set up on root.
logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)
if not logger.handlers:
    _handler = logging.StreamHandler()
    _handler.setFormatter(logging.Formatter("%(levelname)s %(message)s"))
    logger.addHandler(_handler)

DEFAULT_TRANSACTION = {
    "merchant": "Cofide Payments Ltd",
    "category": "Transfer",
    "amountPence": 1500,
}


def handler(event, context):
    transaction = event if isinstance(event, dict) and event.get("merchant") else DEFAULT_TRANSACTION
    webhook_url = os.environ["BANK_SERVER_WEBHOOK_URL"]
    auth_mode = os.environ.get("AUTH_MODE", "static")

    if auth_mode == "spiffe":
        logger.info("Authenticating to bank-server mechanism=jwt_svid caller=bank-lambda")
        token = _exchange_for_jwt_svid()
    elif auth_mode == "static":
        logger.info("Authenticating to bank-server mechanism=static caller=bank-lambda")
        token = os.environ["STATIC_WEBHOOK_API_KEY"]
    else:
        raise ValueError(f"invalid AUTH_MODE: {auth_mode}")

    status, body = _post_transaction(webhook_url, transaction, token)
    if status >= 400:
        logger.warning("bank-server rejected request status=%s body=%s", status, body)
    else:
        logger.info("bank-server accepted request status=%s", status)
    return {"statusCode": status, "body": body}


def _post_transaction(webhook_url, transaction, token):
    req = urllib.request.Request(
        webhook_url,
        data=json.dumps(transaction).encode(),
        headers={
            "Content-Type": "application/json",
            "Authorization": f"Bearer {token}",
            "User-Agent": "cofide-bank-demo-lambda/1.0",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(req) as r:
            return r.status, r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode("utf-8", errors="replace")


def _exchange_for_jwt_svid():
    import boto3

    token_exchange_url = os.environ["TOKEN_EXCHANGE_URL"]
    # Credex's bespoke exchange endpoint mints the outbound JWT-SVID's
    # audience as a literal pass-through of whatever audience is requested
    # here (see exchange/exchange/oidc/oidc.go in the Credex repo) — so this
    # must match bank-server's webhookAudience constant, not just identify
    # Credex as the token's recipient.
    credex_audience = os.environ.get("CREDEX_AUDIENCE", "bank-server-webhook")

    sts = boto3.client("sts")
    resp = sts.get_web_identity_token(
        Audience=[credex_audience],
        SigningAlgorithm="RS256",
        DurationSeconds=300,
    )
    aws_jwt = resp["WebIdentityToken"]
    logger.info(
        "Obtained AWS identity JWT sub=%s (asserted by AWS STS; not yet verified by Credex)",
        _decode_claim(aws_jwt, "sub"),
    )

    body = json.dumps({"InboundToken": aws_jwt}).encode()
    req = urllib.request.Request(
        token_exchange_url,
        data=body,
        headers={
            "Content-Type": "application/json",
            "User-Agent": "cofide-bank-demo-lambda/1.0",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(req) as r:
            payload = json.loads(r.read())
    except urllib.error.HTTPError as e:
        logger.warning(
            "Credex rejected AWS identity JWT status=%s body=%s",
            e.code,
            e.read().decode("utf-8", errors="replace"),
        )
        raise

    svid = payload["token"]
    logger.info(
        "Credex exchanged AWS identity for a JWT-SVID sub=%s (verified by Credex; bank-server will independently re-verify)",
        _decode_claim(svid, "sub"),
    )
    return svid


def _decode_claim(jwt, claim):
    payload = json.loads(base64.urlsafe_b64decode(jwt.split(".")[1] + "=="))
    return payload.get(claim, "<missing>")
