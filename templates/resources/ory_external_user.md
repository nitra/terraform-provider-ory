---
page_title: "ory_external_user Resource — nitra/ory"
subcategory: "Users"
description: |-
  External member організації через захищений execution API.
---

# ory_external_user

Create/read/delete/import external member через `user_api`; тип, роль й організаційні дозволи перевіряє backend. Не надає доступу до Kratos Admin API.

Ця сторінка є static source у templates: tfplugindocs v0.25 припускає один resource prefix, а provider зберігає legacy `hydra_*` разом із новим `ory_*`. Зміни schema потребують оновлення цієї сторінки; unit test перевіряє наявність усіх schema attributes у документації.

## Schema

| Поле | Тип | Вимога | Призначення |
| --- | --- | --- | --- |
| `organization_id` | String | Required | Organization slug; незмінний після create/import |
| `email` | String | Required | Канонічний email у нижньому регістрі, без пробілів; незмінний |
| `create_request_id` | String | Required | Стабільний UUID запиту, зафіксований у Git; незмінний |
| `name` | String | Optional | Ім’я до 120 символів; незмінне після create/import |
| `deletion_reason` | String | Optional+Computed | 8–500 символів; default `Видалення через OpenTofu`; змінюється окремим apply без remote mutation |
| `id` | String | Computed | Стабільний UUID identity |
| `state` | String | Computed | Поточний стан identity; provider його не змінює |

Зміна immutable fields викликає plan error, не replacement. Для DELETE використовується email та остання причина зі state.

## Example

```hcl
provider "hydra" {
  user_api {}
}

resource "ory_external_user" "example" {
  provider          = hydra
  organization_id   = "abie-ua"
  email             = "example@example.com"
  create_request_id = "00000000-0000-4000-8000-000000000004"
}
```

Endpoint і шлях до token file задаються через `ORY_ADMIN_ENDPOINT` та `ORY_ADMIN_TOKEN_FILE`. Не використовуйте `uuid()` у create_request_id.

## Import

Import ID — `organization_id/identity_UUID/create_request_UUID`. Значення мають відповідати HCL; request UUID — локальний bookkeeping, import не викликає POST.

```sh
tofu import ory_external_user.example abie-ua/00000000-0000-4000-8000-000000000003/00000000-0000-4000-8000-000000000004
```

## Межі інтеграції

Live backend integration ще не завершена. Потрібні authoritative GET за UUID, 404 з `error.code=identity_not_found` і CI machine authorization. Generic 404 чи відмова доступу не прибирають identity зі state. Подробиці — guide external-users.
