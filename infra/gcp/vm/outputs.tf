output "instance_id" {
  value = google_compute_instance.this.id
}

output "instance_name" {
  value = google_compute_instance.this.name
}

output "owner" {
  value = var.owner
}

output "agent_name" {
  value = var.agent_name
}

output "environment" {
  value = var.environment
}

output "tracking_id" {
  value = random_id.suffix.hex
}

output "public_ip" {
  value = try(google_compute_instance.this.network_interface[0].access_config[0].nat_ip, "")
}

output "private_ip" {
  value = try(google_compute_instance.this.network_interface[0].network_ip, "")
}

output "connection_info" {
  value = var.network_mode == "public" ? "ssh -i <your-key>.pem ${var.ssh_user}@${google_compute_instance.this.network_interface[0].access_config[0].nat_ip}" : "private IP access: ${google_compute_instance.this.network_interface[0].network_ip}"
}

output "security_group_id" {
  value = google_compute_network.this.id
}

output "security_group_rules" {
  value = local.security_group_rules
}

output "region" {
  value = var.region
}

output "network_mode" {
  value = var.network_mode
}
