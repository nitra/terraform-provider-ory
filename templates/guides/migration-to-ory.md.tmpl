---
page_title: "Міграція nitra/hydra до nitra/ory"
subcategory: "Guides"
description: |-
  Перенесення provider address без зміни Hydra resources чи credentials.
---

# Міграція до nitra/ory

Репозиторій незалежний від GitHub fork network, але містить повну Git history та початкову MIT attribution. Старий `nitra/terraform-provider-hydra` залишається без змін. Успадковані tags позначають історичний код, не releases нового provider. Release assets, PR/issues, settings і secrets не переносяться Git push.

## Передумови

`nitra/ory` ще не опублікований у registry: команди нижче є планом майбутнього переходу, не інструкцією до негайного production запуску. Спочатку потрібні підписаний release, доступність provider у registry та acceptance перевірка сумісності. Live state в рамках перенесення repo не змінювався.

До міграції зупиніть паралельні apply, перевірте точний backend/workspace та зробіть захищений backup state. Не виводьте state у logs: він може містити секрети. State locking має залишатися увімкненим.

## Конфігурація

Змініть тільки source; залиште локальне ім’я `hydra`, resource names та provider aliases:

```hcl
terraform {
  required_providers {
    hydra = {
      source  = "nitra/ory"
      version = "~> 1.1"
    }
  }
}
```

Schemas та `hydra_*` resource names не змінюються. Це також дозволяє зберегти `provider "hydra"` і наявні module provider mappings.

Після публікації та перевірки release:

```sh
tofu state replace-provider registry.opentofu.org/nitra/hydra registry.opentofu.org/nitra/ory
tofu init
tofu plan
```

Спершу `tofu providers` підтверджує реальну стару адресу зі state: якщо hostname інший, використовуйте саме його, не наведену адресу навмання. `replace-provider` змінює provider binding, не resources; автоматичний backup не замінює попередню захищену резервну копію.

Прийнятний результат — `No changes`. Create/update/delete/replace, втрата credentials або невідповідність schemas зупиняють міграцію. Не виконуйте apply, import чи видалення identities для виправлення перейменування.

## Публікація

- Новий repo потребує перевірки доступу до signing secrets, branch protection і release permissions; значення секретів не копіюють через logs/CLI arguments.
- Перенесені tags не перевидаються під новою назвою. Перший новий release — окремий tag після погодження.
- Для `nitra/ory` потрібне окреме registry metadata, з посиланням на новий repo і signing key.
- Архівування старого fork — окрема дія після переходу всіх споживачів.
