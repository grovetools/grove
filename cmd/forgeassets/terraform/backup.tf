# The forge's off-VM backup target.
#
# Everything here is inert when backup_enabled is false, which is the default.
# That matters more than it looks: main.tf item 3 makes "a dedicated service
# account with no IAM roles, attached with no OAuth scopes" the module's
# headline posture, and this file is the ONE place that widens it. The widening
# is deliberately narrow and auditable:
#
#   * one OAuth scope — devstorage.read_write, not cloud-platform. A compromised
#     VM can mint a token that speaks GCS and nothing else.
#   * one IAM binding — objectAdmin on THIS bucket, granted to the forge's own
#     service account. Not project-wide storage admin, which is what the trial
#     had by accident via the default compute SA and what the module exists to
#     have closed.
#
# So the blast radius of "backups are on" is: this bucket's objects. The bucket
# is uniform-access (ACLs are not merely unused but structurally unavailable)
# and public access is prevented at the bucket level, so nothing here can be
# made world-readable by a later `gsutil acl` mistake.

# Creating the bucket and being allowed to use it are separate concerns, and
# the restore drill is what makes that concrete: a REPLACEMENT forge has to read
# artifacts out of a bucket the forge it is replacing already owns. It must
# therefore get the IAM binding and the scope without trying to create — and
# without adopting into its own state — a bucket that exists. The same split is
# what lets several forges share one bucket, namespaced by [forge.backup] prefix.
resource "google_storage_bucket" "backups" {
  count = var.backup_enabled && var.backup_create_bucket ? 1 : 0

  name     = var.backup_bucket
  project  = var.project_id
  location = var.backup_location

  # No ACLs, ever. Uniform bucket-level access makes IAM the only authority,
  # which is what lets the single binding below be the complete access story.
  uniform_bucket_level_access = true
  public_access_prevention    = "enforced"

  # Versioning is what makes an overwrite survivable. The backup script never
  # deletes, and blob mirroring runs without --delete-unmatched precisely so
  # server-side GC cannot propagate deletions into the backup; versioning plus
  # the lifecycle rules below are what keeps that from growing without bound.
  versioning {
    enabled = true
  }

  lifecycle_rule {
    condition {
      age = var.backup_nearline_days
    }
    action {
      type          = "SetStorageClass"
      storage_class = "NEARLINE"
    }
  }

  lifecycle_rule {
    condition {
      days_since_noncurrent_time = var.backup_noncurrent_days
      with_state                 = "ARCHIVED"
    }
    action {
      type = "Delete"
    }
  }

  lifecycle_rule {
    condition {
      age = var.backup_retention_days
    }
    action {
      type = "Delete"
    }
  }

  labels = {
    managed-by = "grove-forge"
    forge      = var.vm_name
  }
}

resource "google_storage_bucket_iam_member" "backups_writer" {
  count = var.backup_enabled ? 1 : 0

  # Named, not referenced through the resource: the binding has to work whether
  # this module created the bucket or is joining one that already exists.
  bucket = var.backup_bucket
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${local.service_account_email}"
}
