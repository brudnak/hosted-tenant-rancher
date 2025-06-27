terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "5.53.0"
    }
  }
  backend "s3" {}
}

provider "aws" {
  region     = "us-east-2"
  access_key = var.aws_access_key
  secret_key = var.aws_secret_key
}

# Variables - only declare what we need at root level
variable "total_rancher_instances" {
  type        = number
  description = "Total number of Rancher instances (host + tenants)"
  default     = 2
}

variable "aws_access_key" {
  type        = string
  description = "AWS access key"
}

variable "aws_secret_key" {
  type        = string
  description = "AWS secret key"
}

variable "aws_prefix" {
  type        = string
  description = "Prefix for resource names"
}

variable "aws_vpc" {
  type        = string
  description = "VPC ID"
}

variable "aws_subnet_a" {
  type        = string
  description = "Subnet A ID"
}

variable "aws_subnet_b" {
  type        = string
  description = "Subnet B ID"
}

variable "aws_subnet_c" {
  type        = string
  description = "Subnet C ID"
}

variable "aws_ami" {
  type        = string
  description = "AMI ID for instances"
}

variable "aws_subnet_id" {
  type        = string
  description = "Subnet ID for instances"
}

variable "aws_security_group_id" {
  type        = string
  description = "Security group ID"
}

variable "aws_pem_key_name" {
  type        = string
  description = "Name of the PEM key for SSH access"
}

variable "aws_rds_password" {
  type        = string
  description = "RDS password"
  sensitive   = true
}

variable "aws_route53_fqdn" {
  type        = string
  description = "Route53 FQDN for DNS records"
}

variable "aws_ec2_instance_type" {
  type        = string
  description = "EC2 instance type"
  default     = "m5.large"
}

# Module configuration
locals {
  rancher_instances = {
    for i in range(1, var.total_rancher_instances + 1) : i => {
      name  = "high-availability-infrastructure-${i}"
      index = i
    }
  }
}

module "high-availability-infrastructure" {
  for_each = local.rancher_instances
  source   = "./modules/k3s-ha"

  # Pass variables to the module - no provider needed since root manages it
  aws_prefix            = "${var.aws_prefix}-${each.key}"  # Make each instance unique
  aws_vpc               = var.aws_vpc
  aws_subnet_a          = var.aws_subnet_a
  aws_subnet_b          = var.aws_subnet_b
  aws_subnet_c          = var.aws_subnet_c
  aws_ami               = var.aws_ami
  aws_subnet_id         = var.aws_subnet_id
  aws_security_group_id = var.aws_security_group_id
  aws_pem_key_name      = var.aws_pem_key_name
  aws_rds_password      = var.aws_rds_password
  aws_route53_fqdn      = var.aws_route53_fqdn
  aws_ec2_instance_type = var.aws_ec2_instance_type
}

# Outputs - following the same pattern as ha-rancher-rke2 repo
output "rancher_details" {
  value = {
    for idx, instance in module.high-availability-infrastructure : "infra_${idx}" => {
      server1_ip     = instance.server1_ip
      server2_ip     = instance.server2_ip
      mysql_endpoint = instance.mysql_endpoint
      mysql_password = instance.mysql_password
      rancher_url    = instance.rancher_url
    }
  }
  sensitive = true
}

output "flat_outputs" {
  value = merge([
    for idx, instance in module.high-availability-infrastructure : {
      (format("infra%d_server1_ip", idx))     = instance.server1_ip
      (format("infra%d_server2_ip", idx))     = instance.server2_ip
      (format("infra%d_mysql_endpoint", idx)) = instance.mysql_endpoint
      (format("infra%d_mysql_password", idx)) = instance.mysql_password
      (format("infra%d_rancher_url", idx))    = instance.rancher_url
    }
  ]...)
  sensitive = true
}