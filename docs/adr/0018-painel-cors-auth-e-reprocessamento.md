# ADR 0018: Painel: CORS, autenticação do SSE e reprocessamento da DLQ

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** N7, Fases 2, 6 e 8 (`docs/plan-go.md`)

## Contexto

`EventSource` e `<img>` não enviam `Authorization`. O reprocessamento da DLQ mexe na idempotência e num lote já completo.

## Decisão

Hipóteses: CORS com allowlist de origens no ingest e no `ms-api`; token estático de demonstração (**não é segurança real**, o README diz isso); SSE via `fetch` com cabeçalho; miniaturas com URL não adivinhável (UUID) ou cookie de demonstração. `POST /dlq/{id}/retry` marca a foto `pending`, grava nova linha no outbox e o evento; o `batch.completed` **não** é reemitido e a reabertura aparece no status da foto. hipótese: **não decidida** até a evidência e a sua aprovação.

## Alternativas

- Cookie de sessão para tudo.
- `EventSource` com token na query string (vaza em log).
- Reemitir `batch.completed` no reprocessamento.

## Custo

Token estático não protege de verdade; URL não adivinhável é segurança por obscuridade.

## Critério de reversão

Se o painel passar a ser exposto fora do ambiente local, trocar por autenticação real.

## Evidência

Pendente. Testes da Fase 6 (origem fora da allowlist e requisição sem token recusadas, SSE incluído; reprocessar sem duplicar resultado). Decidir **antes** da Fase 6.
