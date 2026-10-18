# Changelog

## [Unreleased]

### Added

- Exact `kratos_admin_allowed_remote_endpoints` для trusted cluster executors: loopback-only default збережено, remote access лише через явний provider config, без wildcard/environment opt-in або redirects.
- Тести endpoint policy, provider configuration та реального OpenTofu plan на isolated GET-only fixture; release/consumer activation виконуються окремо.

- `ory_platform_identity` через прямий `kratos_admin` для локального port-forward: import/read/update наявних identities, без create/delete чи перенесення credentials.
- Three-way merge незалежних JSON-шляхів і зупинка при зміні одного шляху; `observed_document` відокремлено від бажаної конфігурації.
- Acceptance tests зі справжнім OpenTofu: import без mutation, незалежний зовнішній drift, update, конфлікт saved plan та збереження state після denial.

## [1.2.0] - 2026-10-09

### Security

- Оновлено `go-jose`, `x/crypto`, `x/net`, `x/text` та gRPC до виправлених версій разом із потрібними транзитивними залежностями.
- Мінімальна версія Go для збірки, CI та release підвищена до `1.26.9`, яка містить виправлення standard library; minor-версію Go збережено.

### Added

- Початковий `ory_external_user`: create, authoritative read, delete та import через захищений execution API.
- Окремі endpoint/token file для user API, без перенесення bearer у resource state чи redirect.
- Стабільний create_request_id у HCL, блокування update identity та implicit replacement.
- HTTP unit tests і mock acceptance lifecycle зі справжнім OpenTofu; live backend integration ще не виконана.

## [1.1.0] - 2026-10-09

### Changed

- Код та історію перенесено в незалежний `nitra/terraform-provider-ory`; походження й ліцензії збережені.
- Go module, binary та provider address підготовлені для `registry.opentofu.org/nitra/ory`.
- Існуючі `hydra_*` resources, локальне ім’я `hydra` та schemas збережені без змін.
- Додано інструкцію безпечної міграції state; release і registry publication ще не виконані.

## Unreleased

### Changed
- Release artifacts are limited to `darwin_amd64`, `darwin_arm64`, `linux_amd64`,
  `linux_arm64` and `windows_amd64` (was 15 GOOS/GOARCH combinations incl. freebsd and
  32-bit). Already published versions keep all their archives.

## 1.0.0 (2026-10-03)

First release of the `nitra/hydra` fork of `svrakitin/terraform-provider-hydra`.

BREAKING CHANGES (vs. upstream 0.5.x):

* Provider address `registry.opentofu.org/nitra/hydra`; rewritten on terraform-plugin-framework (protocol 6).
* Removed `hydra_jwks` resource and data source (private keys in state).
* `hydra_oauth2_client`: `client_id` is required; `client_secret` removed in favour of write-only
  `client_secret_wo` + `client_secret_wo_version` (OpenTofu >= 1.11); `metadata` and `jwks` are JSON strings.

FEATURES:

* API client generated from Ory Hydra v26.2.0 (vendored in `internal/httpclient`).
* `hydra_oauth2_client`: all v26.2.0 fields incl. `skip_consent`, `skip_logout_consent`,
  `access_token_strategy` and the 13 lifespans; semantic equality for durations and JSON;
  drift detection (404 -> re-create).
* New resource `hydra_trusted_jwt_grant_issuer` and data source `hydra_trusted_jwt_grant_issuers`.
* Acceptance tests and empirical Hydra v26.2.0 checks against a local `oryd/hydra:v26.2.0` (podman/docker compose).
