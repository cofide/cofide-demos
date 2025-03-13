data "tls_certificate" "spire_oidc_discovery_provider_tls_certificate" {
  url = "https://${var.spire_oidc_discovery_provider_domain}/keys"
}

resource "aws_iam_openid_connect_provider" "iam_openid_connect_provider" {
  url = "https://${var.spire_oidc_discovery_provider_domain}"
  client_id_list = [
    "${var.spire_jwt_svid_audience}"
  ]
  thumbprint_list = [data.tls_certificate.spire_oidc_discovery_provider_tls_certificate.certificates[0].sha1_fingerprint]
}
