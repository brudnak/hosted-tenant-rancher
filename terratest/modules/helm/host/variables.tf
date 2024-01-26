variable "rancher_url" {}
variable "repository_url" {}
variable "bootstrap_password" {}
variable "rancher_version" {
  default = ""
}
variable "rancher_image" {
  default = "rancher/rancher"
}
variable "image_tag" {
  default = ""
}
variable "psp_bool" {
  default = false
}