terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.53.0"
    }
    archive = {
      source  = "hashicorp/archive"
      version = "~> 2.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
}
