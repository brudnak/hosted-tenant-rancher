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
  value = module.high-availability-infrastructure-1.mysql_password
  sensitive = true
}

output "infra1_rancher_url" {
  value = module.high-availability-infrastructure-1.rancher_url
}

