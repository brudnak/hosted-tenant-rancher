// Infrastructure 1 section
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


// Infrastructure 2 section
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
  value = module.high-availability-infrastructure-2.mysql_password
  sensitive = true
}

output "infra2_rancher_url" {
  value = module.high-availability-infrastructure-2.rancher_url
}
