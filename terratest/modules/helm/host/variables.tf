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

variable "env_name_0" {
  description = "Name of the first extra environment variable"
  type        = string
  default     = ""
}

variable "env_value_0" {
  description = "Value of the first extra environment variable"
  type        = string
  default     = ""
}

variable "env_name_1" {
  description = "Name of the first extra environment variable"
  type        = string
  default     = ""
}

variable "env_value_1" {
  description = "Value of the first extra environment variable"
  type        = string
  default     = ""
}
