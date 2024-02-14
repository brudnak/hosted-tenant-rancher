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
variable "psp_enabled" {
  default = false
}

variable "extra_env_name" {
  description = "Name of the first extra environment variable"
  type        = string
  default     = ""
}

variable "extra_env_value" {
  description = "Value of the first extra environment variable"
  type        = string
  default     = ""
}