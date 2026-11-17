# ADR 0007: Banco e acesso a dados

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** D5 (`docs/plan-go.md`)

## Contexto

O relay do outbox precisa de `SELECT ... FOR UPDATE SKIP LOCKED` (SQLite não tem). O v1 usava MySQL com Eloquent.

## Decisão

PostgreSQL com `pgx` e `sqlc` (SQL tipado e gerado, sem ORM). hipótese: **não decidida** até a evidência e a sua aprovação.

## Alternativas

- `database/sql` puro.
- ORM (GORM e similares): descartado por esconder o SQL do outbox.
- MySQL 8 (`SKIP LOCKED` existe, mas não é a escolha do plano).

## Custo

Geração de código (`sqlc`) no fluxo de build; Postgres a mais para operar localmente.

## Critério de reversão

`database/sql` puro bastar para o volume do projeto. Não é justificativa que Postgres seja melhor no `testcontainers-go` (há módulo oficial para os dois).

## Evidência

Pendente. S4 (relay com `SKIP LOCKED` e duas instâncias).
