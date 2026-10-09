terraform {
  # write-only arguments (client_secret_wo) need OpenTofu >= 1.11
  required_version = ">= 1.11.0"
  required_providers {
    hydra = {
      source  = "nitra/ory"
      version = "~> 1.0"
    }
  }
}

# The Admin API (port 4445) is normally only reachable in-cluster / via
# port-forward. The endpoint can also come from HYDRA_ADMIN_URL.
provider "hydra" {
  endpoint = "http://hydra-admin.hydra.svc:4445"

  retry_policy {
    enabled = true
  }
}
