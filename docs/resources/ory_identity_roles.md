# ory_identity_roles

Керує повним впорядкованим `metadata_public.roles` наявної Kratos identity. У state зберігаються лише UUID і ролі; повний профіль, metadata та credentials не зберігаються цим ресурсом. UUID та ролі теж розкривають інформацію про доступ; використовуйте приватний repository і захищений state.

```hcl
resource "ory_identity_roles" "user" {
  provider = hydra
  roles    = ["gt", "abie:dagster"]
  lifecycle { prevent_destroy = true }
}

import {
  provider = hydra
  to       = ory_identity_roles.user
  id       = "00000000-0000-4000-8000-000000000001"
}
```

Задайте повний список ролей у чинному порядку: перша роль може визначати default role. Import читає Kratos і не змінює identity. Спочатку імпортуйте з наявним списком і перевірте no-op plan, потім окремим plan додайте потрібні ролі. Після refresh plan показує drift і повертає ролі до конфігурації. Відсутня identity не відтворюється; створення та видалення заборонено. Для припинення керування використовуйте `removed` з `lifecycle { destroy = false }`.

PATCH змінює лише `/metadata_public/roles`, інші поля не керуються. `metadata_public` має бути об'єктом; null потребує окремого приймання. Не керуйте цим самим списком одночасно через `ory_platform_identity` або іншим writer. Перед записом ресурс перевіряє ролі повторним GET і відхиляє розбіжність із plan, але перевірка й PATCH не є атомарними: узгоджуйте одночасні записи та перевіряйте результат після timeout. Для production import і apply діють окремі adoption та approval gates.
