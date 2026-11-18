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

Pendente. Fase 7 (métricas); o isolamento de falha de destino é coberto pela ADR 0020.
