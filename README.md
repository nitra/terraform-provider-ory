# OpenTofu / Terraform provider nitra/ory

Незалежний provider від Nitra для **self-hosted Ory**, не офіційний `ory/ory` для Ory Network.
Код перенесено з `nitra/terraform-provider-hydra` зі збереженням Git history, tags і ліцензій.
Початковий код походить від [svrakitin/terraform-provider-hydra](https://github.com/svrakitin/terraform-provider-hydra); зв’язку з GitHub fork network цей repo не має.

## Статус перенесення

Provider address підготовлено для `registry.opentofu.org/nitra/ory`, Go module — `github.com/nitra/terraform-provider-ory`.
Новий provider ще не опублікований: registry installation та live state migration не виконані.
Історичні tags не є releases `nitra/ory`; старий fork і його release assets залишаються без змін.
Підписаний release, registry registration, signing secrets, branch protection та перехід споживачів — окремі етапи.

## Реалізовані можливості

Provider працює через Hydra Admin API; server/client прив’язані до `oryd/hydra:v26.2.0`.
Використовується `terraform-plugin-framework`, protocol 6, без legacy SDK/mux.

| Тип | Назва | Призначення |
| --- | --- | --- |
| Resource | `hydra_oauth2_client` | OAuth2 clients, token lifespans, write-only client secret |
| Resource | `hydra_trusted_jwt_grant_issuer` | Trust relationships для RFC 7523 jwt-bearer |
| Data source | `hydra_trusted_jwt_grant_issuers` | Перегляд trusts та ID для import |

Локальне ім’я `hydra` та існуючі `hydra_*` schemas збережені для сумісності.
Додано початковий `ory_external_user` через окремий блок `user_api`. Provider перевірено через mock API та реальний OpenTofu; live інтеграція ще потребує backend GET/absence contract і CI machine authorization. Докладніше — [external users](docs/guides/external-users.md).

Документація: [docs/index.md](docs/index.md), [міграція до nitra/ory](docs/guides/migration-to-ory.md), [Forgejo Actions → Hydra](docs/guides/forgejo-jwt-bearer.md).

## Конфігурація після публікації

```hcl
terraform {
  required_version = ">= 1.11.0"
  required_providers {
    hydra = {
      source  = "nitra/ory"
      version = "~> 1.1"
    }
  }
}

provider "hydra" {
  endpoint = "http://hydra-admin.hydra.svc:4445"
}
```

Hydra Admin API має бути доступним тільки довіреному executor.
Для перенесення state потрібні backup, правильний backend/workspace, `replace-provider` та наступний `No changes` plan. Не виконуйте migration до доступності нового підписаного release.

## Гарантії й обмеження

- `client_secret_wo` та `client_secret_wo_version` не зберігають client secret у resource state.
- JWKS resource відсутній, щоб не переносити private keys у state; public JWKS читається через HTTP data source.
- JSON і durations порівнюються семантично, зокрема після import.
- Після out-of-band видалення OAuth2 client наступний plan може запропонувати повторне створення.
- Вилучення Optional+Computed server-default параметра з HCL зберігає його останнє значення; для зміни задайте значення явно.
- За empirical tests Hydra v26.2.0 trust expiry не спрацьовує для assertion із `kid`; відкликання потребує видалення trust.
- Видалення trust може каскадно видалити інші trusts зі спільним `(issuer, kid)`: не використовуйте `create_before_destroy`, уникайте спільного ключа для різних subjects.

## Розробка

Потрібні Go із `go.mod`, OpenTofu >= 1.11 і Podman/Docker compose для acceptance tests.

```sh
make build
make lint
make test
make docs
make hydra-up
make testacc
```

Docker можна обрати через `COMPOSE="docker compose"`. Acceptance tests створюють і видаляють тестові ресурси: запускайте їх тільки проти ізольованої локальної Hydra, не production. Локальний container cleanup — `make hydra-down`.

## Release

GoReleaser готує archives для darwin/linux amd64/arm64 та windows amd64, SHA256SUMS, GPG signature і protocol 6 manifest.
Потрібно окремо перевірити доступ нового repo до signing credentials `GPG_PRIVATE_KEY` / `PASSPHRASE` та registration `nitra/ory` у OpenTofu Registry.
Не перепубліковуйте успадковані tags: перший новий release потребує нового tag після успішного CI та погодження власника.

## Ліцензії

- MIT: [LICENSE](LICENSE), Copyright (c) 2021 Stepan Rakitin та Copyright (c) 2026 nitra.
- Vendored Ory Hydra client: Apache-2.0, [internal/httpclient/LICENSE](internal/httpclient/LICENSE), походження у [README-VENDOR.md](internal/httpclient/README-VENDOR.md).
- Патерн write-only secret/version відповідає підходу `ory/terraform-provider-ory`; код із нього не копіювався.
