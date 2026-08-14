# Цифровой нотариус

MVP платформы ЭДО для заказчиков и самозанятых. Она фиксирует согласие с Соглашением ЭДО, создаёт неизменяемый журнал действий, выдаёт ссылку на подписание и поддерживает два маршрута: ПЭП по одноразовому коду и внешний провайдер УКЭП.

> Это технический MVP, не юридическое заключение. Для запуска в РФ необходимы заключение с аккредитованным УЦ/оператором ЭДО и договоры с провайдерами идентификации.

## Быстрый старт

```powershell
go run ./cmd/api
```

Сервер слушает `:8080`. Укажите `PORT`, `SIGNING_URL_BASE` и `SMS_DEV_CODE` (только для локальной разработки) при необходимости.

Для локального интеграционного контура запустите `docker compose up -d`, затем `go run ./cmd/migrate` и отдельными процессами `go run ./cmd/outbox-publisher`, `go run ./cmd/notification-dispatcher`, `go run ./cmd/notification-worker`. Все учётные данные в `compose.yaml` предназначены только для разработки.

По умолчанию файлы сохраняются только в памяти — это режим разработки. Чтобы направить файлы в выделенный удалённый gateway, укажите `OBJECT_STORAGE_URL` и, если нужен, `OBJECT_STORAGE_TOKEN`. Для mTLS укажите пути `STORAGE_CLIENT_CERT`, `STORAGE_CLIENT_KEY` и опционально `STORAGE_CA_CERT`. В production Vault Agent записывает эти значения в `/vault/secrets/app.env`; исходный файл не пишется на диск Pod.

## API-сценарий

1. `POST /v1/auth/sms/request` с `phone` — запрашивает код. В dev-режиме код возвращается в ответе.
2. `POST /v1/auth/sms/verify` — создаёт bearer-токен. В production вместо этого подключаются Сбер ID / ЕСИА (OIDC) и SMS-провайдер.
3. `POST /v1/documents` — заказчик создаёт документ, явно принимая `edoAgreementVersion`.
4. `PUT /v1/documents/{id}/file` — до отправки можно загрузить файл до 10 МБ; его SHA-256 попадёт в доказательственный пакет.
5. `POST /v1/documents/{id}/send` — формирует одноразовую ссылку исполнителю.

Оригинал скачивается через `GET /v1/documents/{id}/file` только участниками сделки. Перед выдачей сервис сверяет его SHA-256 с хешем, зафиксированным в документе.
5. Исполнитель вызывает `POST /v1/signing/{token}/pep/request`, затем `.../pep/confirm`; заказчик — `POST /v1/documents/{id}/ukep/start`.

Контракт API находится в [openapi.yaml](openapi.yaml). Миграция БД — в [migrations/001_initial.sql](migrations/001_initial.sql).

## Границы безопасности

- ПЭП допустима только при заранее принятом всеми участниками соглашении ЭДО; в доказательственный пакет пишутся идентификатор пользователя, IP, user-agent, OTP challenge, время и хеш документа.
- УКЭП не реализуется «самодельно»: сервис лишь передаёт хеш в адаптер аккредитованного провайдера и хранит его результат.
- Сами файлы не лежат в Pod или PostgreSQL: Document Service работает через отдельный S3-совместимый endpoint в изолированном облачном контуре. Метаданные содержат ключ и SHA-256.
- В Kubernetes секреты, OAuth credentials и клиентские сертификаты выдаёт Vault Agent; в репозиторий они не попадают. Ротация сертификатов и CRL/OCSP проверка — обязательная часть production-интеграции.

## Монетизация

Сервис использует подписку без комиссии с каждой сделки. Каталог тарифов доступен через `GET /v1/billing/plans`: «Старт», «Бизнес» и «Корпоративный». Подключение эквайринга и выдача оплаченных entitlements выполняются отдельным Billing Service после выбора платёжного провайдера.

В production создание документа требует активной или trial-подписки. Лимит тарифа и счётчик использованных документов проверяются и обновляются атомарно при сохранении документа.

## Состав

- `cmd/api` — Document/Auth API и доказательственный журнал MVP.
- `internal` — доменная модель, подпись, аудит и HTTP-слой.
- `deploy/k8s` — Namespace, PostgreSQL, RabbitMQ, API и NetworkPolicy.
- `infra/vault` — политика и пример конфигурации выдачи секретов.
- `.github/workflows/ci.yml` — тесты, контейнерная сборка и deployment в Kubernetes через GitHub Actions/OIDC.

## Production-порядок

1. Развернуть PostgreSQL, Kafka, RabbitMQ, Vault и object-storage gateway в закрытом контуре.
2. Передать приложению `DATABASE_URL` и storage-параметры через Vault, настроить backup/restore и мониторинг. Миграции применяются командой `go run ./cmd/migrate` или Kubernetes Job `deploy/k8s/migrate-job.yaml`.
3. Подключить SMS/Email, OIDC (Сбер ID/ЕСИА) и провайдера УКЭП в staging с их тестовыми ключами.
4. Провести нагрузочные тесты, внешний аудит безопасности и пилот с ограниченным числом компаний.

Kubernetes-контур включает три API-реплики, HPA (3–12 реплик по CPU) и PodDisruptionBudget с минимум двумя доступными Pod. Для HPA в кластере должен быть установлен Metrics Server.

CI публикует production-образ в GHCR только из `main` и выкатывает immutable tag, равный SHA коммита. Это позволяет откатить deployment на точный предыдущий образ.

`deploy/overlays/staging` и `deploy/overlays/production` содержат Kustomize-overlays. Новые подписочные планы и интеграции сначала проверяются в staging с отдельными Vault-секретами, а затем продвигаются в production.

Точные production-входы и checklist перед запуском описаны в `docs/production-handoff.md`.

Полный перечень реализованного и оставшихся этапов: `docs/project-status.md`.

Текущая версия уже ограничивает срок жизни сессии (24 часа), подписной ссылки (48 часов) и каждого OTP (10 минут). В production эти записи переносятся из памяти в PostgreSQL/Redis.

Для production сервисы выделяются из этого MVP в Auth, Document, Signing, Notification и PDF Generator. Изменения статусов публикуются через transactional outbox в Kafka; фоновые задачи — в RabbitMQ.

## Сбер ID

Демо-авторизация отключена. Вход запускается через `GET /v1/auth/sber/start` и завершается callback-ом `https://tailly.ru/v1/auth/sber/callback`. Реализация использует authorization-code flow, PKCE, `state`/`nonce`, проверку подписи ID token по JWKS и HttpOnly-сессию. Для включения production-входа заполните в Kubernetes Secret параметры `SBER_ID_*` и `OIDC_STATE_HMAC_KEY` из партнёрского кабинета Сбер ID; список находится в `.env.example`.
