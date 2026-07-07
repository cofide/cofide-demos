resource "aws_iam_role" "bank_lambda" {
  name = "${var.function_name}-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy_attachment" "bank_lambda_basic" {
  role       = aws_iam_role.bank_lambda.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole"
}

# Only needed when the Lambda exchanges its own AWS identity for a JWT-SVID
# via Cofide Credex.
resource "aws_iam_role_policy" "bank_lambda_web_identity_token" {
  count = var.auth_mode == "spiffe" ? 1 : 0

  name = "${var.function_name}-sts-web-identity-token"
  role = aws_iam_role.bank_lambda.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = ["sts:GetWebIdentityToken"]
      Resource = "*"
    }]
  })
}
