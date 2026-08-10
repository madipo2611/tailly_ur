# Статус проекта «Цифровой нотариус»

Актуально на 2026-08-10.

## Реализовано

### Основной пользовательский сценарий

- SMS-вход в демо-режиме, нормализация и валидация российских номеров.
- Создание документа по версионированному шаблону: договор ГПХ, акт, NDA, гарантийное письмо.
- Загрузка оригинала документа до 10 МБ, SHA-256 фиксация и проверка целостности при скачивании.
- Одноразовая ссылка на подписание с TTL 48 часов.
- ПЭП исполнителя: SMS-код, принятие соглашения ЭДО, доказательственный audit-event.
- Маршрут УКЭП: запуск и HMAC-защищённый callback внешнего провайдера. Публичное завершение УКЭП отключено.
- Доказательственный пакет API: документ, hash-chain аудита, ПЭП/УКЭП-подписи и endpoint проверки целостности.
- Личный кабинет API: список документов с ограничением выдачи, статус, скачивание оригинала.

### Постоянное хранение

- PostgreSQL-миграции: документы, пользователи, подписи, аудит, outbox, OTP, сессии, подписные ссылки, подписки, индексы и версия шаблона.
- PostgreSQL является источником чтения документов после перезапуска API.
- Сессии, OTP и ссылки хранятся только в виде SHA-256-хешей.
- Сессии можно отозвать на одном или всех устройствах.
- Cleanup CronJob удаляет истёкшие security-записи.
- Adapter object storage: memory для разработки, HTTP gateway + bearer token/mTLS для production.

### События и уведомления

- Transactional outbox при изменении документа.
- Kafka publisher с at-least-once доставкой и event ID для дедупликации.
- Kafka dispatcher создаёт задачи уведомлений в RabbitMQ.
- RabbitMQ worker доставляет SMS в настроенный HTTP SMS gateway; без настройки работает в log-режиме.

### Подписка

- Каталог тарифов без комиссии за сделку.
- PostgreSQL-модель подписки: план, статус, использованный лимит и расчётный период.
- Создание документа с PostgreSQL требует active/trial-подписку и атомарно списывает лимит.
- HMAC Billing webhook создаёт/обновляет подписку; повторы webhook не сбрасывают лимит без смены периода/тарифа.

### Security и эксплуатация

- TTL: OTP 10 минут, сессия 24 часа, ссылка подписания 48 часов.
- Пять неверных OTP-анонсов аннулируют код.
- Rate limiting API, security headers, CORS allow-list, `Cache-Control: no-store` для API.
- Correlation ID, `/healthz`, `/readyz`, `/metrics`, structured HTTP logs.
- Graceful shutdown, PostgreSQL pool settings.
- Kubernetes: Secrets для runtime-конфигурации, NetworkPolicy для API, probes, HPA, PDB, resource quota, LimitRange, seccomp, non-root/read-only containers.
- CI: unit/smoke/race tests, `go vet`, dependency sync, PostgreSQL migration check, immutable GHCR SHA images and rollout.

### Развёрнутый контур

- `document-api` развёрнут в namespace `digital-notary`: 3 реплики готовы, PostgreSQL работает внутри Kubernetes на постоянном томе 10 GiB.
- Схема БД применена отдельным migration Job; доступ API к БД ограничен pod-сетью и ролью `digital_notary`.
- Ingress опубликован через балансировщик `89.108.100.190`; проверка `/healthz` и `/readyz` через него возвращает HTTP 200.
- Конфигурация приложения хранится в Kubernetes Secrets. Внешний доступ API к объектному хранилищу ограничен Cilium FQDN-политикой только для `s3.regru.cloud`.

## Требует внешних интеграций

Это не блокеры кода, но без них нельзя запустить реальный production-трафик.

- Сбер ID/ЕСИА или другой OIDC provider: issuer, client ID/secret, redirect URI, JWKS policy.
- Выбранный SMS-gateway: production endpoint, token, delivery reports и договор.
- Аккредитованный оператор/провайдер УКЭП: API старта подписи, callback contract, callback secret, сертификатная/OCSP policy.
- Изолированный object-storage gateway: URL, token/mTLS, lifecycle/retention, backup/restore.
- Billing/эквайринг: платёжный провайдер и HMAC event contract.
- Production-интеграции Kafka и RabbitMQ, когда будут включены фоновые обработчики.

## Остаётся реализовать до production-пилота

1. Подключить реальные OIDC, SMS, УКЭП, storage и Billing integrations в staging.
2. Изменить DNS-записи `A` для `tailly.ru` и `www.tailly.ru` на `89.108.100.190`; после распространения DNS выпустить TLS-сертификат.
3. Реализовать PDF Generator Service: утверждённые шаблоны, кириллические шрифты, статический архивный evidence PDF; визуально проверить рендер.
4. Добавить email-канал уведомлений и delivery reports/SMS retry-DLQ policy.
5. Сделать cursor pagination, фильтры и поиск в личном кабинете; подключить их к веб-интерфейсу.
6. Добавить integration tests с PostgreSQL, Kafka, RabbitMQ и object-storage gateway, а также нагрузочные и отказоустойчивые тесты.
7. Настроить production monitoring/alerting: 5xx, readiness, DB pool, outbox lag, Kafka consumer lag, глубина RabbitMQ и ошибки delivery.
8. Проверить backup/restore PostgreSQL и object storage, провести staging-пилот с 20–30 компаниями.
9. Провести юридическую экспертизу соглашения ЭДО и финальных текстов шаблонов. Техническая реализация ПЭП не заменяет такую экспертизу.

## Команды проверки

```powershell
go test ./...
go vet ./...
go run ./cmd/migrate
```

Для локального интеграционного контура используется `docker compose up -d`; детали описаны в `README.md`.
