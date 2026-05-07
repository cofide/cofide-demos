variable "project_id" {
  description = "The ID of the GCP project."
  type        = string
}

variable "workload_identity_pool_id" {
  description = "The ID for the created workload identity pool (should map to the trust domain)."
  type        = string
}

variable "oidc_endpoint_for_trust_zone" {
  description = "The URL from which the OIDC discovery provider is being served without the schema (i.e., the https:// prefix)."
  type        = string
}

variable "audience" {
  description = "The JWT audience (aud) claim that identifies the recipients that the JWT is intended for."
  type        = string
}

variable "consumer_spiffe_id" {
  description = "SPIFFE ID of the consumer workload."
  type = string
}
