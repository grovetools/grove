terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
  }
}

provider "google" {
  project = var.project_id
}

data "google_compute_image" "os" {
  family  = var.image_family
  project = var.image_project
}

resource "google_compute_firewall" "satellite_ssh" {
  name    = "${var.vm_name}-allow-ssh"
  network = "default"

  allow {
    protocol = "tcp"
    ports    = ["22"]
  }

  source_ranges = [var.allowed_ssh_cidr]
  target_tags   = [var.vm_name]
}

resource "google_compute_instance" "satellite" {
  name         = var.vm_name
  machine_type = var.machine_type
  zone         = var.zone
  tags         = [var.vm_name]

  boot_disk {
    initialize_params {
      image = data.google_compute_image.os.self_link
      size  = var.disk_size_gb
      type  = "pd-balanced"
    }
  }

  network_interface {
    network = "default"

    access_config {
      # ephemeral external IP
    }
  }

  # Optional attached service account: the VM authenticates to GCP via the
  # metadata server (Application Default Credentials) — no JSON keys are
  # ever created or copied to disk. Omitted entirely when the email is "".
  dynamic "service_account" {
    for_each = var.service_account_email == "" ? [] : [var.service_account_email]

    content {
      email  = service_account.value
      scopes = var.service_account_scopes
    }
  }

  metadata = {
    ssh-keys = "${var.ssh_user}:${file(pathexpand(var.ssh_pubkey_file))}"
    startup-script = templatefile("${path.module}/startup.sh.tpl", {
      ssh_user    = var.ssh_user
      zig_version = var.zig_version
    })
  }
}
