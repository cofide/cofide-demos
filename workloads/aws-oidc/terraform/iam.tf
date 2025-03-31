resource "aws_iam_role" "iam_role_oidc_discovery_provider" {
  name = "${var.project_name}-oidc-discovery-provider-role"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = "sts:AssumeRoleWithWebIdentity"
        Effect = "Allow"
        Principal = {
          Federated = "arn:aws:iam::${local.aws_account_id}:oidc-provider/${var.spire_oidc_discovery_provider_domain}"
        }
        Condition = {
          StringEquals = {
            "${var.spire_oidc_discovery_provider_domain}:aud" = "${var.spire_jwt_svid_audience}",
            "${var.spire_oidc_discovery_provider_domain}:sub" = "spiffe://${var.trust_domain}${var.consumer_spiffe_id_path}"
          }
        }
      }
    ]
  })

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_iam_role_policy" "iam_role_policy_oidc_discovery_provider" {
  name = "${var.project_name}-oidc-discovery-provider-role-policy"
  role = aws_iam_role.iam_role_oidc_discovery_provider.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Action = [
          "s3:ListAllMyBuckets",
          "s3:ListBucket",
        ]
        Effect   = "Allow"
        Resource = "*"
      },
    ]
  })

  lifecycle {
    create_before_destroy = true
  }
}
