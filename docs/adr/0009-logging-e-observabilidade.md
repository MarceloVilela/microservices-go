# ADR 0009: Logging e observabilidade

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** RQ4, Fase 7 (`docs/plan-go.md`)

## Contexto

O v1 derrubava a rota principal quando o Loggly falhava. O projeto precisa de logs estruturados com correlação e métricas básicas.

## Decisão

`log/slog` em JSON, com `correlation_id`/`batch_id`; nunca logar segredo ou token. **Loggly descartado** (decidido em 2026-11-17; o v1 nunca confirmou eventos lá). Notificação por destinos fica na ADR 0020. Métricas (profundidade das filas, jobs por status, latência por job) via `expvar` ou Prometheus, decidido na Fase 7. hipótese: **não decidida** até a evidência e a sua aprovação.

## Alternativas

- Manter o Loggly como sink opcional (descartado: sem conta confirmada, sem valor para o portfólio).
- Biblioteca de log de terceiros: sem ganho sobre `slog`.

## Custo

Prometheus adiciona dependência; sem Loggly, não há log centralizado externo (aceito: só local).

## Critério de reversão

Se passar a existir necessidade de log centralizado, reabrir com nova ADR.

## Evidência

**Fase 7 (2026-11-18):** `platform/metrics` (registro Prometheus próprio por serviço, `GET /metrics` sem Bearer, rótulo `route` = padrão do `ServeMux`, nunca o caminho cru; teste e aceite provam que nenhum UUID vira rótulo). Métricas: `photos_processed_total{outcome}` e `photo_process_seconds{outcome}` (worker), `queue_messages{queue}` (declaração passiva com cache de 5 s; broker fora ⇒ a métrica some e o scrape segue 200), `outbox_pending`/`outbox_quarantined`/`outbox_oldest_pending_age_seconds` (ingest), `photos_by_status{status}`, `sse_clients`, `sse_last_seq` (api), `notifications_total{outcome}`. Dependência nova só em `platform/` e nos `main`: `client_golang`. Faltam alertas e dashboards (fora do escopo). O isolamento de falha de destino é coberto pela ADR 0020. **Segue `proposta`** até a aprovação.
