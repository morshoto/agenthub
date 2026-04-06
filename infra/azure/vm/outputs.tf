output "instance_id" {
  value = azurerm_linux_virtual_machine.this.id
}

output "instance_name" {
  value = azurerm_linux_virtual_machine.this.name
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
  value = azurerm_public_ip.this.ip_address
}

output "private_ip" {
  value = azurerm_network_interface.this.private_ip_address
}

output "connection_info" {
  value = var.network_mode == "public" ? "ssh -i <your-key>.pem ${var.ssh_user}@${azurerm_public_ip.this.ip_address}" : "private IP access: ${azurerm_network_interface.this.private_ip_address}"
}

output "security_group_id" {
  value = azurerm_network_security_group.this.id
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
