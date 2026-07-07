variable "aws_region" {
  description = "The AWS region to deploy the Lambda function into."
  type        = string
}

variable "function_name" {
  description = "Name of the Lambda function."
  type        = string
  default     = "cofide-bank-demo-lambda"
}

variable "auth_mode" {
  description = "Auth mode for calls to bank-server: \"static\" (pre-shared API key) or \"spiffe\" (JWT-SVID minted by Cofide Credex)."
  type        = string

  validation {
    condition     = contains(["static", "spiffe"], var.auth_mode)
    error_message = "auth_mode must be either \"static\" or \"spiffe\"."
  }
}

variable "bank_server_webhook_url" {
  description = "URL of bank-server's webhook endpoint (e.g. http://<external-address>:8444/webhook/transactions), reachable from AWS."
  type        = string
}

variable "static_webhook_api_key" {
  description = "Pre-shared API key sent as a bearer token when auth_mode = \"static\". Must match bank-server's STATIC_WEBHOOK_API_KEY."
  type        = string
  default     = ""
  sensitive   = true
}

variable "token_exchange_url" {
  description = "URL of the Cofide Credex token exchange endpoint, used when auth_mode = \"spiffe\"."
  type        = string
  default     = ""
}

variable "credex_audience" {
  description = "Audience claim requested on the AWS web identity token presented to Credex."
  type        = string
  default     = "cofide-credex"
}
