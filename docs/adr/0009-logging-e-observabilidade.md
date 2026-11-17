# ADR 0009: Logging e observabilidade

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** RQ4, Fase 7 (`docs/plan-go.md`)

## Contexto

O v1 derrubava a rota principal quando o Loggly falhava. O projeto precisa de logs estruturados com correlação e métricas básicas.

## Decisão

`log/slog` em JSON, com `correlation_id`/`batch_id`; nunca logar segredo ou token. **Loggly, se mantido, é sink opcional que nunca derruba o fluxo.** Métricas (profundidade das filas, jobs por status, latência por job) via `expvar` ou Prometheus, decidido na Fase 7. hipótese: **não decidida** até a evidência e a sua aprovação.

## Alternativas

- Descartar o Loggly (decisão em aberto 4 do plano).
- Biblioteca de log de terceiros: sem ganho sobre `slog`.

## Custo

Sink opcional exige isolar falha e registrá-la; Prometheus adiciona dependência.

## Critério de reversão

Se o Loggly continuar sem confirmar eventos na conta do v1, descartar o sink.

## Evidência

Pendente. Fase 5 (falha do destino de log não propaga) e Fase 7.
