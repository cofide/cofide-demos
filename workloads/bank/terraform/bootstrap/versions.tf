terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.53.0"
    }
    ory = {
      source  = "ory/ory"
      version = "~> 26.2.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

// The project API key is deliberately not a Terraform variable — it's a
// live credential, not config. Export it as ORY_PROJECT_API_KEY (the
// provider reads it directly from the environment) rather than passing it
// through -var/tfvars, where it would end up in shell history, CI logs, or
// a committed file if someone's not careful.
provider "ory" {
  project_slug = var.ory_project_slug
}
