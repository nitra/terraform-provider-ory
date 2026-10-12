---
page_title: "ory_platform_identity Resource — nitra/ory"
subcategory: "Users"
description: |-
  Імпорт і керування всіма configurable полями наявних internal/admin identities.
---

# ory_platform_identity

Керує наявною identity через окремий приватний `platform_api`, включно з internal
users та admins. Google machine authorization перевіряє backend; tenant tokens
не мають платформних прав. UUID зберігається; credentials та sessions лишаються
у Kratos. Resource підтримує import/read/update. Create/recreate та фізичне delete
заблоковані у фазі adoption; для припинення керування використайте `removed` із
`lifecycle { destroy = false }`.

Static source потрібен через змішані prefixes `hydra_*`/`ory_*` у tfplugindocs v0.25.

## Schema

| Поле | Тип | Вимога | Призначення |
| --- | --- | --- | --- |
| `id` | String | Computed | Наявний UUID Kratos identity |
| `document` | String (JSON), Sensitive | Required | Усі configurable поля identity |
| `revision` | String | Computed | Fingerprint документа й updated_at; перевіряється перед записом |

Document обов’язково містить `schema_id`, `state`, `traits`, `metadata_public`,
`metadata_admin`, `verifiable_addresses`, `recovery_addresses`, `external_id`,
`organization_id`. Metadata допускають object/null, addresses — arrays,
external/native organization IDs — string/null. Roles і memberships керуються
через metadata. Credentials, UUID, schema_url та server-generated timestamps
не допускаються в document. Чутливі значення не показуються в звичайному plan,
але зберігаються у state: потрібні приватний Git repository та захищений backend.
Server-owned `id`, `created_at`, `updated_at` усередині address objects теж
не входять до document. При зміні email задайте відповідні нові address values.

## Приклад

```hcl
terraform {
  required_providers {
    hydra = { source = "nitra/ory" }
  }
}

provider "hydra" {
  platform_api {
    endpoint   = "https://private-platform-api.example.com"
    token_file = "/run/secrets/platform-google-id.token"
  }
}

resource "ory_platform_identity" "user" {
  provider = hydra
  document = jsonencode({
    schema_id = "default"
    state = "active"
    traits = { email = "example@example.com", user_type = "internal" }
    metadata_public = { roles = ["admin"] }
    metadata_admin = null
    verifiable_addresses = []
    recovery_addresses = []
    external_id = null
    organization_id = null
  })
  lifecycle { prevent_destroy = true }
}

import {
  provider = hydra
  to = ory_platform_identity.user
  id = "00000000-0000-4000-8000-000000000001"
}
```

Для імпорту задавайте фактичний повний document наявної identity, а не дані
з прикладу. Перший normal plan має бути без змін. Запит використовує тільки
GET під час import; provider не додає adoption marker і не змінює user_type.

`ORY_PLATFORM_ENDPOINT` та `ORY_PLATFORM_TOKEN_FILE` можуть задавати конфігурацію
в environment, але блок `platform_api {}` обов’язковий. Token file перечитується
перед кожним запитом; token не потрапляє в HCL/state. Redirect і retry mutation
відсутні; HTTPS обов’язковий, HTTP дозволений лише для loopback rehearsal.

## Конкурентні зміни й recovery

Stale revision повертає 409; provider зберігає state та зупиняє apply.
При відсутності імпортованої identity Read повертає diagnostic і не запускає
recreate. 401/403, generic 404, timeout та 5xx також не очищають state.

Kratos v26.2.0 відхиляє JSON Patch `test`. Перевірка revision перед PATCH не є
атомарним CAS для сторонніх writers; перед cutover їх треба узгодити/зупинити.
Server-derived адреси можуть нормалізуватися після зміни email/schema — перевірте
результат і наступний normal plan. Production import та ввімкнення live mutations
потребують окремого приймання після rehearsal.
