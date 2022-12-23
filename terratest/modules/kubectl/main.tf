terraform {
  required_providers {
    kubectl = {
      source = "gavinbunney/kubectl"
      version = "1.14.0"
    }
  }
}

provider "kubectl" {
  host                   = var.config_ip
  load_config_file       = true
}

data "http" "myfile" {
  url = var.manifest_url

}
resource "kubectl_manifest" "test" {
  yaml_body = data.http.myfile.response_body
}

variable "config_ip" {}
variable "manifest_url" {}

