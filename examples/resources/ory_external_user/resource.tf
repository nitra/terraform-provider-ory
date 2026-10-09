terraform {
  required_providers {
    hydra = { source = "nitra/ory" }
  }
}

provider "hydra" {
  user_api {
    endpoint   = "https://id.example.com"
    token_file = "/run/secrets/ory-admin-access.token"
  }
}

resource "ory_external_user" "example" {
  provider          = hydra
  organization_id   = "abie-ua"
  email             = "example@example.com"
  name              = "Приклад"
  create_request_id = "00000000-0000-4000-8000-000000000004"
  deletion_reason   = "Видалення через OpenTofu"
}
