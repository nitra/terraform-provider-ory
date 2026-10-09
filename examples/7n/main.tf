# Complete example for the 7n setup:
#   * OAuth2 clients declared as Hydra client JSON in hydra/clients/*.json,
#     adopted once with `import` blocks;
#   * Forgejo Actions OIDC tokens exchanged for Hydra access tokens via the
#     RFC 7523 jwt-bearer grant: one trust per Forgejo signing key (kid);
#   * a new confidential client whose secret comes from Infisical through an
#     ephemeral resource and a write-only argument (never stored in state).
#
# Requires OpenTofu >= 1.11 (write-only arguments, ephemeral resources,
# for_each in import blocks).

terraform {
  required_version = ">= 1.11.0"
  required_providers {
    hydra = {
      source  = "nitra/ory"
      version = "~> 1.0"
    }
    http = {
      source  = "hashicorp/http"
      version = "~> 3.4"
    }
    infisical = {
      source  = "Infisical/infisical"
      version = ">= 0.15.0"
    }
  }
}

variable "hydra_admin_url" {
  type    = string
  default = "http://hydra-admin.hydra.svc:4445"
}

variable "infisical_project_id" {
  type = string
}

# Bump after rotating HYDRA_CLIENT_SECRET_APICURIO_SYNC in Infisical: the
# write-only secret is only sent to Hydra when this number changes.
variable "apicurio_sync_secret_version" {
  type    = number
  default = 1
}

provider "hydra" {
  endpoint = var.hydra_admin_url
}

# Infisical credentials come from INFISICAL_* environment variables.
provider "infisical" {}

# ---------------------------------------------------------------------------
# Clients from hydra/clients/*.json (Hydra client JSON, as `hydra get client -o json`)
# ---------------------------------------------------------------------------
locals {
  clients = {
    for f in fileset("${path.module}/hydra/clients", "*.json") :
    trimsuffix(f, ".json") => jsondecode(file("${path.module}/hydra/clients/${f}"))
  }
}

resource "hydra_oauth2_client" "this" {
  for_each = local.clients

  client_id                  = each.value.client_id
  client_name                = try(each.value.client_name, null)
  token_endpoint_auth_method = try(each.value.token_endpoint_auth_method, null)
  grant_types                = try(each.value.grant_types, null)
  response_types             = try(each.value.response_types, null)
  scope                      = try(each.value.scope, null)
  audience                   = try(each.value.audience, null)
  redirect_uris              = try(each.value.redirect_uris, null)
  post_logout_redirect_uris  = try(each.value.post_logout_redirect_uris, null)
  allowed_cors_origins       = try(each.value.allowed_cors_origins, null)
  skip_consent               = try(each.value.skip_consent, null)
  metadata                   = try(jsonencode(each.value.metadata), null)

  access_token_strategy                          = try(each.value.access_token_strategy, null)
  client_credentials_grant_access_token_lifespan = try(each.value.client_credentials_grant_access_token_lifespan, null)
  jwt_bearer_grant_access_token_lifespan         = try(each.value.jwt_bearer_grant_access_token_lifespan, null)
}

# One-time adoption of clients that already exist in Hydra. Import of a
# client that does not exist fails, so remove a key from this map (or the
# whole block) once it is in state. Existing confidential clients keep their
# secret: an imported client is never re-created and PUT without a secret
# keeps the stored hash.
import {
  for_each = local.clients
  to       = hydra_oauth2_client.this[each.key]
  id       = each.value.client_id
}

# ---------------------------------------------------------------------------
# Forgejo Actions -> Hydra (jwt-bearer)
# ---------------------------------------------------------------------------
data "http" "forgejo_jwks" {
  url             = "https://git.7n.ai/api/actions/.well-known/keys"
  request_headers = { Accept = "application/json" }

  lifecycle {
    # An empty/failed JWKS would plan the destruction of every trust.
    postcondition {
      condition     = self.status_code == 200 && length(try(jsondecode(self.response_body).keys, [])) > 0
      error_message = "Forgejo JWKS is unavailable or empty - refusing to plan trust changes."
    }
  }
}

locals {
  forgejo_keys = { for k in jsondecode(data.http.forgejo_jwks.response_body).keys : k.kid => k }
}

resource "hydra_trusted_jwt_grant_issuer" "forgejo" {
  for_each = local.forgejo_keys

  # Must equal the `iss` claim of Forgejo Actions OIDC tokens.
  issuer  = "https://git.7n.ai/api/actions"
  subject = "repo:7n-45/rules-132:ref:refs/heads/main"
  scope   = ["apicurio:developer"]
  jwk     = jsonencode(each.value)

  # Fixed, ~10 years ahead, deliberately not rotated (no time_rotating):
  # trust lifetime is managed by Forgejo's key rotation (a kid that disappears
  # from the JWKS destroys its trust). Note: Hydra v26.2.0 does not enforce
  # expires_at for assertions that carry a `kid` header anyway.
  expires_at = "2036-10-01T00:00:00Z"

  # Deliberately NO `create_before_destroy`:
  #  * Hydra has a unique index on (issuer, subject, key_id): the replacement
  #    would collide with the old trust (HTTP 409) whenever the key is the same;
  #  * deleting a trust also deletes the JWK (issuer, kid) it shares with any
  #    other trust, and the FK ... ON DELETE CASCADE silently deletes those
  #    trusts too - so the old trust's delete would wipe the new one.
}

# Public client used by the CI job (client_id in the form body, no secret).
resource "hydra_oauth2_client" "forgejo_ci" {
  client_id                  = "forgejo-ci-rules-132"
  client_name                = "Forgejo Actions (7n-45/rules-132)"
  token_endpoint_auth_method = "none"
  grant_types                = ["urn:ietf:params:oauth:grant-type:jwt-bearer"]
  scope                      = "apicurio:developer"

  jwt_bearer_grant_access_token_lifespan = "10m"
}

# ---------------------------------------------------------------------------
# New confidential client with its secret from Infisical (never in state)
# ---------------------------------------------------------------------------
ephemeral "infisical_secret" "apicurio_sync" {
  name         = "HYDRA_CLIENT_SECRET_APICURIO_SYNC"
  env_slug     = "prod"
  workspace_id = var.infisical_project_id
  folder_path  = "/hydra"
}

resource "hydra_oauth2_client" "apicurio_sync" {
  client_id   = "apicurio-sync"
  grant_types = ["client_credentials"]
  scope       = "apicurio:developer"

  client_secret_wo = ephemeral.infisical_secret.apicurio_sync.value
  # Ephemeral values cannot feed non-write-only arguments, so the rotation
  # trigger is a plain variable (Infisical's `version` is ephemeral too).
  client_secret_wo_version = var.apicurio_sync_secret_version

  client_credentials_grant_access_token_lifespan = "15m"
}

# Alternative without the Infisical provider: an ephemeral input variable,
# e.g. TF_VAR_apicurio_sync_secret=$(infisical secrets get ... --plain)
#
# variable "apicurio_sync_secret" {
#   type      = string
#   sensitive = true
#   ephemeral = true
# }
