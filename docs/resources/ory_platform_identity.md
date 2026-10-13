---
page_title: "ory_platform_identity Resource — nitra/ory"
subcategory: "Users"
description: |-
  Імпорт і керування всіма configurable полями наявних internal/admin identities.
---

# ory_platform_identity

Керує наявною identity через Kratos Admin API, доступний на локальному
`kubectl port-forward`. UUID зберігається; credentials та sessions лишаються
у Kratos. Resource підтримує import/read/update. Create/recreate та фізичне delete
заблоковані у фазі adoption; для припинення керування використайте `removed` із
`lifecycle { destroy = false }`.

Static source потрібен через змішані prefixes `hydra_*`/`ory_*` у tfplugindocs v0.25.

## Schema

| Поле | Тип | Вимога | Призначення |
| --- | --- | --- | --- |
| `id` | String | Computed | Наявний UUID Kratos identity |
| `document` | String (JSON), Sensitive | Required | Усі configurable поля identity |
| `observed_document` | String (JSON), Sensitive | Computed | Останній прочитаний стан Kratos |
| `revision` | String | Computed | Fingerprint документа й updated_at для спостереження |

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
  kratos_admin = "http://127.0.0.1:15434"
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

`KRATOS_ADMIN_URL` може задавати endpoint замість `kratos_admin`. HTTP дозволений
лише через loopback `kubectl port-forward`; публічний маршрут не потрібен.
Redirect і retry mutation відсутні.

## Конкурентні зміни й recovery

`document` у state зберігає попередню бажану конфігурацію; `observed_document`
оновлюється під час refresh. Коли ви змінюєте `document`, provider порівнює
стару конфігурацію, нову конфігурацію і поточний Kratos. Незалежні зміни
JSON-шляхів зберігаються, зміна одного шляху з обох боків зупиняє apply.
Arrays (зокрема roles і addresses) є одним неподільним шляхом. Конфлікт
показується без значень персональних даних. Наступний plan без вашої зміни
не повертає зовнішні зміни до старих значень HCL.
Якщо потрібно навмисно повернути значення, яке Kratos змінив зовні, спочатку
синхронізуйте відповідний шлях `document` із поточним Kratos, застосуйте
цей no-op plan, потім встановіть бажане значення і виконайте окремий plan/apply.

При відсутності імпортованої identity Read повертає diagnostic і не запускає
recreate. Помилки доступу, timeout та 5xx також не очищають state.

Kratos v26.2.0 відхиляє JSON Patch `test`. Перевірка потрібних шляхів перед PATCH
не є атомарним CAS; одночасний запис саме під час PATCH потребує окремого
узгодження writers. Після timeout перечитайте identity перед повторною дією.
Server-derived адреси можуть нормалізуватися після зміни email/schema — перевірте
результат і наступний normal plan. Production import та ввімкнення live mutations
потребують окремого приймання після rehearsal.
