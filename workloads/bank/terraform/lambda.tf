data "archive_file" "bank_lambda" {
  type        = "zip"
  source_file = "${path.module}/../bank-lambda/handler.py"
  output_path = "${path.module}/bank-lambda.zip"
}

locals {
  auth_mode_env = var.auth_mode == "spiffe" ? {
    TOKEN_EXCHANGE_URL = var.token_exchange_url
    CREDEX_AUDIENCE    = var.credex_audience
    } : {
    STATIC_WEBHOOK_API_KEY = var.static_webhook_api_key
  }
}

resource "aws_lambda_function" "bank_lambda" {
  function_name    = var.function_name
  role             = aws_iam_role.bank_lambda.arn
  runtime          = "python3.14"
  handler          = "handler.handler"
  filename         = data.archive_file.bank_lambda.output_path
  source_code_hash = data.archive_file.bank_lambda.output_base64sha256
  timeout          = 10

  environment {
    variables = merge(
      {
        AUTH_MODE               = var.auth_mode
        BANK_SERVER_WEBHOOK_URL = var.bank_server_webhook_url
      },
      local.auth_mode_env,
    )
  }
}
