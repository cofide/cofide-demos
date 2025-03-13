variable "aws_region" {
  description = "The name of the AWS region."
  type        = string
}

variable "k8s_namespace" {
  description = "The name of the Kubernetes namespace where workloads that need to assume the IAM role reside."
  type        = string
}

variable "project_name" {
  description = "The name of the project."
  type        = string
}

variable "spire_oidc_discovery_provider_domain" {
  description = "The domain from which the SPIRE OIDC discovery provider is being served (exclude the https:// prefix)."
  type        = string
}

variable "spire_jwt_svid_audience" {
  description = "The JWT audience (aud) claim that identifies the recipients that the JWT is intended for."
  type        = string
}

variable "trust_domain" {
  description = "The trust domain where the workloads are deployed."
  type        = string
}
