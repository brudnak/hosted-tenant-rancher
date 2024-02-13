terraform {
  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = "2.12.1"
    }
  }
}

provider "helm" {
  kubernetes {
    config_path = "../../../../host.yml"
  }
}

resource "helm_release" "rancher" {
  name             = "rancher"
  repository       = var.repository_url
  chart            = "rancher"
  version          = var.rancher_version
  create_namespace = "true"
  namespace        = "cattle-system"

  set {
    name  = "hostname"
    value = var.rancher_url
  }

  set {
    name  = "global.cattle.psp.enabled"
    value = var.psp_enabled
  }

  set {
    name  = "rancherImage"
    value = var.rancher_image
  }

  set {
    name  = "rancherImageTag"
    value = var.image_tag
  }

  set {
    name  = "bootstrapPassword"
    value = var.bootstrap_password
  }

  set {
    name  = "tls"
    value = "external"
  }

  set {
    name  = "extraEnv[0].name"
    value = var.extra_env_name
  }

  set {
    name  = "extraEnv[0].value"
    value = var.extra_env_value
  }
}
