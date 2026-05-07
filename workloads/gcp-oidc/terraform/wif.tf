resource "google_iam_workload_identity_pool" "trust_zone" {
  workload_identity_pool_id = var.workload_identity_pool_id
}

resource "google_iam_workload_identity_pool_provider" "trust_zone" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.trust_zone.workload_identity_pool_id
  workload_identity_pool_provider_id = "connect"
  attribute_mapping = {
    "google.subject" = "assertion.sub"
  }
  oidc {
    issuer_uri        = "https://${var.oidc_endpoint_for_trust_zone}"
    allowed_audiences = [var.audience]
  }
}
