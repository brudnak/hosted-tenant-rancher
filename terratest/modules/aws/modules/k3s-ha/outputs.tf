output "server1_ip" {
  value = aws_instance.aws_instance[0].public_ip
}

output "server2_ip" {
  value = aws_instance.aws_instance[1].public_ip
}

output "mysql_password" {
  value     = var.aws_rds_password
  sensitive = true
}

output "mysql_endpoint" {
  value = aws_rds_cluster_instance.aws_rds_cluster_instance[0].endpoint
}

output "rancher_url" {
  value = aws_route53_record.aws_route53_record.fqdn
}
