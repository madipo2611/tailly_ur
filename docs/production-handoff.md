# Production handoff

Кодовая база готова к подключению внешних систем через Vault. Перед production rollout владелец сервиса предоставляет следующие значения и доступы.

| Интеграция | Что требуется |
|---|---|
| PostgreSQL | `DATABASE_URL`, backup policy и доступ миграционного Job |
| Object storage gateway | `OBJECT_STORAGE_URL`, сервисный токен или mTLS client cert/key/CA |
| SMS | `SMS_GATEWAY_URL`, `SMS_GATEWAY_TOKEN`, SLA и callback delivery reports |
| UKЭП | endpoint старта, callback URL, `UKEP_WEBHOOK_SECRET`, формат provider reference, сертификаты/OCSP policy |
| OIDC | issuer, client ID/secret, redirect URI, JWKS policy для Сбер ID/ЕСИА |
| Billing | HMAC secret `BILLING_WEBHOOK_SECRET`, event contract и расписание продления подписки |
| Kafka/RabbitMQ | broker URL, TLS/SASL credentials, retention, DLQ policy |

Все значения хранятся в Vault в `kv/data/digital-notary/api` и монтируются Vault Agent. Их нельзя добавлять в Git, `.env.example` или Docker image.

## Перед включением трафика

1. Выполнить migration Job на staging и проверить rollback backup.
2. Прогнать сквозной сценарий: создание - ПЭП - callback УКЭП - evidence - уведомление.
3. Проверить лимиты подписки через Billing webhook и повторную доставку события.
4. Настроить alerting для readiness, 5xx, ошибок outbox и глубины RabbitMQ queue.
