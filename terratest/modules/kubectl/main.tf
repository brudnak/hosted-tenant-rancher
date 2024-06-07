terraform {}

resource "null_resource" "deploy-yaml" {

provisioner local-exec {
  interpreter = ["/bin/bash" ,"-c"]
  command = <<-EOT
    export KUBECONFIG=tenant_kube_config.yml
    kubectl apply -f ${var.manifest_url}
  EOT
  }
}

variable "config_ip" {}
variable "manifest_url" {}

