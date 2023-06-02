terraform {
  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = "2.7.1"
    }
  }
}

provider "helm" {
  kubernetes {
    config_path = "../../../../host.yml"
  }
}

resource "helm_release" "certs" {
  name             = "cert-manager"
  repository       = "https://charts.jetstack.io"
  chart            = "cert-manager"
  version          = "1.7.1"
  namespace        = "cert-manager"
  create_namespace = "true"

  set {
    name  = "installCRDs"
    value = "true"
  }
}

resource "helm_release" "rancher" {
  name             = "rancher"
  repository       = "https://releases.rancher.com/server-charts/latest"
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
    value = var.psp_bool
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
    name  = "ingress.tls.source"
    value = "letsEncrypt"
  }

  set {
    name  = "letsEncrypt.email"
    value = var.email
  }

  set {
    name  = "letsEncrypt.ingress.class"
    value = "traefik"
  }

  # wait for certs to be installed first
  depends_on = [
    helm_release.certs
  ]
}
