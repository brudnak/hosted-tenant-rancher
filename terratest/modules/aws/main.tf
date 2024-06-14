terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "5.53.0"
    }
  }
  backend "s3" {}
}

# dynamic write happens below here
module "high-availability-infrastructure-1" {
  source                = "./modules/k3s-ha"
  aws_prefix            = var.aws_prefix
  aws_access_key        = var.aws_access_key
  aws_secret_key        = var.aws_secret_key
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
module "high-availability-infrastructure-2" {
  source                = "./modules/k3s-ha"
  aws_prefix            = var.aws_prefix
  aws_access_key        = var.aws_access_key
  aws_secret_key        = var.aws_secret_key
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
output "infra1_server1_ip" {
  value = module.high-availability-infrastructure-1.server1_ip
}
output "infra1_server2_ip" {
  value = module.high-availability-infrastructure-1.server2_ip
}
output "infra1_mysql_endpoint" {
  value = module.high-availability-infrastructure-1.mysql_endpoint
}
output "infra1_mysql_password" {
  value     = module.high-availability-infrastructure-1.mysql_password
  sensitive = true
}
output "infra1_rancher_url" {
  value = module.high-availability-infrastructure-1.rancher_url
}
output "infra2_server1_ip" {
  value = module.high-availability-infrastructure-2.server1_ip
}
output "infra2_server2_ip" {
  value = module.high-availability-infrastructure-2.server2_ip
}
output "infra2_mysql_endpoint" {
  value = module.high-availability-infrastructure-2.mysql_endpoint
}
output "infra2_mysql_password" {
  value     = module.high-availability-infrastructure-2.mysql_password
  sensitive = true
}
output "infra2_rancher_url" {
  value = module.high-availability-infrastructure-2.rancher_url
}
