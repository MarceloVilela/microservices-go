# ADR 0018: Painel: CORS, autenticação do SSE e reprocessamento da DLQ

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** N7, Fases 2, 6 e 8 (`docs/plan-go.md`)

## Contexto

`EventSource` e `<img>` não enviam `Authorization`. O reprocessamento da DLQ mexe na idempotência e num lote já completo.

## Decisão

Escolhas do usuário em 2026-11-18 (`specs/fase-6-api.md`, D1 a D3), **ainda `proposta` até a evidência da Fase 6 e a aprovação**: CORS com allowlist de origens no ingest e no `ms-api`; token estático de demonstração (**não é segurança real**, o README diz isso); SSE via `fetch` com `Authorization` (sem token na query); miniaturas **também com Bearer** (o painel baixa por `fetch` e usa `blob:` URL; sem cookie nem URL pública). `POST /dlq/{id}/retry` só reabre foto `failed` (marca `pending`, grava nova linha no outbox e o evento); `done`/`pending` ⇒ 409. O `batch.completed` **não** é reemitido, `completed_count` não é decrementado (o worker passa a contar o lote só na 1ª finalização da foto) e a reabertura aparece no status da foto.

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
