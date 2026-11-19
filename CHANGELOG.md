# Changelog

Uma entrada por fase (a unidade de entrega do projeto; um PR por fase). Datas de merge na `main`. Detalhe e evidência em `specs/fase-N-*.md` e `specs/execucao-fase-N.log`.

## Fase 9: fechamento (em andamento, branch `fase-9-fechamento`; sem PR)
- `docs/comparacao-v1-go.md`, README reescrito, este `CHANGELOG.md` e checklist de long tail em `specs/fase-9-fechamento.md`. Sem código de serviço.

## Fase 8: painel Angular, opcional (PR #9, 2026-11-19)
- Serviço `web` (nginx, `WITH_WEB=1 ./run.sh`, porta 8080), CORS com a origem do painel, contrato publicado (`specs/openapi-*.yaml`, `specs/events.schema.json`), `scripts/aceite-fase-8.sh`.
- `events.schema.json` validado por biblioteca nos testes. Conserto do aceite 7 (SIGPIPE de `curl | grep -q`).
- O painel mora no repositório irmão `microservices-frontend-angular`.

## Fase 7: observabilidade, segurança e carga (PR #8, 2026-11-19)
- `/metrics` Prometheus nos quatro serviços (`platform/metrics`); foto `pending` por infra esgotada visível em `GET /dlq` e com retry.
- Testes de entrada hostil (`TestHostil*`); carga `make load` (números no README).

## Fase 6: `ms-api` (PR #7, 2026-11-18)
- Leitura de lotes, fotos e DLQ; miniatura com `ETag`; retry da DLQ; SSE com `Last-Event-ID` sobre a tabela `events`; tudo com Bearer.

## Fase 5: `ms-notification` (PR #6, 2026-11-18)
- Consome `batch.completed`; `Notifier` (`slog`, SMTP/Mailpit opcional); dedup por `batch_id` (migração 0003); `amqpx.Supervisor` vai para `platform/`.

## Fase 4: resiliência (PR #5, 2026-11-18)
- Reconexão do consumidor, escada de retry completa, `Retry-After`, shutdown gracioso, DLQ inspecionável (`docs/dlq.md`), "broker reiniciado" (R5 do v1).

## Fase 3: `ms-worker` (PR #4, 2026-11-18)
- Consumo com ack manual, download com defesas de SSRF, miniatura atômica, idempotência por `message_id`, fim de lote numa transação, quarentena de linha venenosa no outbox.

## Fase 2: `ms-ingest` (PR #3, 2026-11-18)
- `POST /batches` com `Idempotency-Key`, Postgres, outbox e relay com `FOR UPDATE SKIP LOCKED` e publisher confirms.

## Fase 1: esqueleto (PR #2, 2026-11-18)
- Monorepo Go (`go.work`), `platform/`, `topology-init`, Dockerfile `distroless`, compose por serviço, CI, lint e teste de arquitetura (G4).

## Fase 0: auditoria, spec e spikes (PR #1, 2026-11-18)
- Auditoria do v1, oráculo de mensageria, dataset, ADRs e spikes S1 a S5 (G0).
