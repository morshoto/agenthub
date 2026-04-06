terraform {
  required_version = ">= 1.6.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = ">= 3.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.6"
    }
  }
}

provider "azurerm" {
  features {}
}

locals {
  location            = trimspace(var.region) != "" ? trimspace(var.region) : "japaneast"
  resource_group_name = "${var.name_prefix}-${random_id.suffix.hex}"
  network_name        = "${var.name_prefix}-${random_id.suffix.hex}-vnet"
  subnet_name         = "${var.name_prefix}-${random_id.suffix.hex}-subnet"
  nsg_name            = "${var.name_prefix}-${random_id.suffix.hex}-nsg"
  nic_name            = "${var.name_prefix}-${random_id.suffix.hex}-nic"
  public_ip_name      = "${var.name_prefix}-${random_id.suffix.hex}-pip"
  owner               = trimspace(var.owner)
  agent_name          = trimspace(var.agent_name)
  environment         = trimspace(var.environment) != "" ? trimspace(var.environment) : "default"
  tracking_id         = random_id.suffix.hex
  instance_name       = "${trimspace(var.name_prefix)}-${local.owner}-${local.agent_name}-${local.environment}-${local.tracking_id}"
  listen_port         = var.runtime_port > 0 ? var.runtime_port : 8080
  runtime_cidr        = trimspace(var.runtime_cidr) != "" ? trimspace(var.runtime_cidr) : trimspace(var.ssh_cidr)
  runtime_provider    = trimspace(var.runtime_provider)
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

resource "azurerm_resource_group" "this" {
  name     = local.resource_group_name
  location = local.location
}

resource "azurerm_virtual_network" "this" {
  name                = local.network_name
  location            = azurerm_resource_group.this.location
  resource_group_name = azurerm_resource_group.this.name
  address_space       = ["10.20.0.0/16"]
}

resource "azurerm_subnet" "this" {
  name                 = local.subnet_name
  resource_group_name  = azurerm_resource_group.this.name
  virtual_network_name = azurerm_virtual_network.this.name
  address_prefixes     = ["10.20.1.0/24"]
}

resource "azurerm_public_ip" "this" {
  name                = local.public_ip_name
  location            = azurerm_resource_group.this.location
  resource_group_name = azurerm_resource_group.this.name
  allocation_method   = "Static"
  sku                 = "Standard"
}

resource "azurerm_network_security_group" "this" {
  name                = local.nsg_name
  location            = azurerm_resource_group.this.location
  resource_group_name = azurerm_resource_group.this.name

  security_rule {
    name                       = "SSH"
    priority                   = 1001
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "22"
    source_address_prefix      = trimspace(var.ssh_cidr)
    destination_address_prefix = "*"
  }

  security_rule {
    name                       = "Runtime"
    priority                   = 1002
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = tostring(local.listen_port)
    source_address_prefix      = local.runtime_cidr
    destination_address_prefix = "*"
  }
}

resource "azurerm_network_interface" "this" {
  name                = local.nic_name
  location            = azurerm_resource_group.this.location
  resource_group_name = azurerm_resource_group.this.name

  ip_configuration {
    name                          = "internal"
    subnet_id                     = azurerm_subnet.this.id
    private_ip_address_allocation = "Dynamic"
    public_ip_address_id          = azurerm_public_ip.this.id
  }
}

resource "azurerm_network_interface_security_group_association" "this" {
  network_interface_id      = azurerm_network_interface.this.id
  network_security_group_id = azurerm_network_security_group.this.id
}

resource "azurerm_linux_virtual_machine" "this" {
  name                = local.instance_name
  location            = azurerm_resource_group.this.location
  resource_group_name = azurerm_resource_group.this.name
  size                = var.instance_type
  admin_username      = var.ssh_user
  network_interface_ids = [
    azurerm_network_interface.this.id,
  ]
  computer_name                   = local.instance_name
  disable_password_authentication = true
  custom_data                     = base64encode(local.user_data)
  disk_size_gb                    = var.disk_size_gb

  os_disk {
    caching              = "ReadWrite"
    storage_account_type = "Standard_LRS"
  }

  source_image_reference {
    publisher = "Canonical"
    offer     = "0001-com-ubuntu-server-jammy"
    sku       = "22_04-lts-gen2"
    version   = "latest"
  }

  admin_ssh_key {
    username   = var.ssh_user
    public_key = trimspace(var.ssh_public_key)
  }

  tags = {
    managed_by  = "agenthub"
    owner       = local.owner
    agent_name  = local.agent_name
    environment = local.environment
    tracking_id = local.tracking_id
  }

  depends_on = [azurerm_network_interface_security_group_association.this]
}
