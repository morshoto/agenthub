terraform {
  required_version = ">= 1.6.0"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = ">= 5.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.6"
    }
  }
}

provider "google" {
  region = var.region
}

locals {
  zone              = trimspace(var.region) != "" ? "${trimspace(var.region)}-a" : "us-central1-a"
  network_name      = "${var.name_prefix}-${random_id.suffix.hex}"
  subnet_name       = "${var.name_prefix}-${random_id.suffix.hex}-subnet"
  instance_tag      = "${var.name_prefix}-${random_id.suffix.hex}"
  owner             = trimspace(var.owner)
  agent_name        = trimspace(var.agent_name)
  environment       = trimspace(var.environment) != "" ? trimspace(var.environment) : "default"
  tracking_id       = random_id.suffix.hex
  instance_name     = "${trimspace(var.name_prefix)}-${local.owner}-${local.agent_name}-${local.environment}-${local.tracking_id}"
  listen_port       = var.runtime_port > 0 ? var.runtime_port : 8080
  runtime_cidr      = trimspace(var.runtime_cidr) != "" ? trimspace(var.runtime_cidr) : trimspace(var.ssh_cidr)
  resolved_image_id = trimspace(var.image_id) != "" ? trimspace(var.image_id) : "projects/ubuntu-os-cloud/global/images/family/ubuntu-2204-lts"
  runtime_provider  = trimspace(var.runtime_provider)
  runtime_config_yaml = yamlencode({
    use_nemoclaw = var.use_nemoclaw
    nim_endpoint = var.nim_endpoint
    model        = var.model
    port         = local.listen_port
    provider     = local.runtime_provider
    region       = var.region
    sandbox = {
      enabled          = true
      network_mode     = var.network_mode
      filesystem_allow = []
    }
  })
  user_data = templatefile("${path.module}/user_data.sh.tftpl", {
    runtime_config_yaml = local.runtime_config_yaml
  })
  security_group_rules = [
    "allow tcp/22 from ${trimspace(var.ssh_cidr)}",
    "allow tcp/${local.listen_port} from ${local.runtime_cidr}",
  ]
}

resource "random_id" "suffix" {
  byte_length = 4
}

resource "google_compute_network" "this" {
  name                    = local.network_name
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "this" {
  name                     = local.subnet_name
  region                   = var.region
  network                  = google_compute_network.this.id
  ip_cidr_range            = "10.10.0.0/24"
  private_ip_google_access = true
}

resource "google_compute_firewall" "ssh" {
  name    = "${local.network_name}-ssh"
  network = google_compute_network.this.id

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  source_ranges = [trimspace(var.ssh_cidr)]
  target_tags   = [local.instance_tag]
}

resource "google_compute_firewall" "runtime" {
  name    = "${local.network_name}-runtime"
  network = google_compute_network.this.id

  allow {
    protocol = "tcp"
    ports    = [tostring(local.listen_port)]
  }

  source_ranges = [local.runtime_cidr]
  target_tags   = [local.instance_tag]
}

resource "google_compute_instance" "this" {
  name         = local.instance_name
  zone         = local.zone
  machine_type = var.instance_type
  tags         = [local.instance_tag]

  allow_stopping_for_update = true

  boot_disk {
    initialize_params {
      image = local.resolved_image_id
      size  = var.disk_size_gb
      type  = "pd-balanced"
    }
  }

  network_interface {
    subnetwork = google_compute_subnetwork.this.id

    access_config {}
  }

  metadata = {
    "ssh-keys" = "${var.ssh_user}:${trimspace(var.ssh_public_key)}"
  }
  metadata_startup_script = local.user_data

  labels = {
    managed_by  = "agenthub"
    owner       = local.owner
    agent_name  = local.agent_name
    environment = local.environment
    tracking_id = local.tracking_id
  }
}
