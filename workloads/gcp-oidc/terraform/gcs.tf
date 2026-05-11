data "google_project" "project" {
}

resource "google_project_iam_member" "storage_bucket_viewer_binding" {
  project = var.project_id
  role    = "roles/storage.bucketViewer"
  member  = "principal://iam.googleapis.com/projects/${data.google_project.project.number}/locations/global/workloadIdentityPools/${google_iam_workload_identity_pool.trust_zone.workload_identity_pool_id}/subject/${var.consumer_spiffe_id}"
}
