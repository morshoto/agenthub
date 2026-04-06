variable "region" {
  type = string
}

variable "aws_profile" {
  type    = string
  default = ""
}

variable "compute_class" {
  type = string
}

variable "owner" {
  type = string
}

variable "agent_name" {
  type = string
}

variable "environment" {
  type    = string
  default = "default"
}

variable "instance_type" {
  type = string
}

variable "disk_size_gb" {
  type = number
}

variable "network_mode" {
  type = string
}

variable "image_id" {
  type = string
}

variable "image_name" {
  type    = string
  default = ""
}

variable "ssh_key_name" {
  type    = string
  default = ""
}

variable "ssh_public_key" {
  type    = string
  default = ""
}

variable "github_private_key_secret_arn" {
  type    = string
  default = ""
}

variable "github_token_secret_arn" {
  type    = string
  default = ""
}

variable "ssh_cidr" {
  type    = string
  default = ""
}

variable "ssh_user" {
  type    = string
  default = "ubuntu"
}

variable "name_prefix" {
  type    = string
  default = "agenthub"
}

variable "use_nemoclaw" {
  type    = bool
  default = false
}

variable "nim_endpoint" {
  type    = string
  default = ""
}

variable "model" {
  type    = string
  default = ""
}

variable "runtime_port" {
  type    = number
  default = 8080
}

variable "runtime_cidr" {
  type    = string
  default = "0.0.0.0/0"
}

variable "runtime_provider" {
  type    = string
  default = ""
}

variable "source_archive_url" {
  type = string
}

variable "container_name" {
  type    = string
  default = "agenthub"
}
