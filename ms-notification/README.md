# ms-notification

Consome `batch.completed` e notifica **uma vez por lote**.

## O que faz

- Consome a fila `batch.completed` com ack manual e reconexão.
- Deduplica por `batch_id` na tabela `notifications` (migração 0003): reentrega do broker não gera segunda notificação.
- Notifica por `Notifier` (ADR 0020): **`slog` sempre**; **SMTP opcional** (no desenvolvimento, o Mailpit captura o e-mail e não envia nada). Falha no destino é registrada e a mensagem recebe ack, então o consumo não para.

## Endpoints (porta 8084)

`GET /healthz`, `GET /readyz`, `GET /metrics`. Não há API de negócio.

## Configuração

| Variável | Padrão | Nota |
|---|---|---|
| `AMQP_URL`, `DATABASE_URL` | obrigatórias | |
| `HTTP_ADDR` | `:8084` | |
| `SMTP_ADDR` | vazio | o compose usa `mailpit:1025`; vazio = só `slog` |
| `SMTP_FROM` / `SMTP_TO` | vazio | |
| `SMTP_TIMEOUT` | `5s` | |
| `SHUTDOWN_TIMEOUT` / `CONSUMER_BACKOFF_MAX` | `10s` / `30s` | |

Mailpit (só desenvolvimento): UI e API em http://127.0.0.1:8025, subido pelo compose deste serviço.

## Estrutura

`internal/app` (serviço), `internal/adapters/{consumer,postgres,slogn,smtpn}`.

## Limites conhecidos

E-mail com `last_error` e reprocesso de notificação falha não existem (rastreado em `specs/fase-5-notification.md`).

## Rodar e testar

`./run.sh` (raiz), `make check`, `make integration`. Spec: `specs/fase-5-notification.md`.
