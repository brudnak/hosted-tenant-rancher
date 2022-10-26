output "server_ip" {
  value = module.ha-1.server_ip
}

output "server_ip2" {
  value = module.ha-1.server_ip2
}

output "db_password" {
  value = module.ha-1.db_password
  sensitive = true
}

output "db_endpoint" {
  value = module.ha-1.db_endpoint
}

output "rancher_url" {
  value = module.ha-1.rancher_url
}

