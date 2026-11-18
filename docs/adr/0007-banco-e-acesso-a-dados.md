# ADR 0007: Banco e acesso a dados

- **Status:** aceita (PostgreSQL + `pgx`, aprovada pelo usuário em 2026-11-18); **`sqlc` adiado**, decidido na Fase 2 (critério abaixo)
- **Data:** 2026-11-17
- **Origem no plano:** D5 (`docs/plan-go.md`)

## Contexto

O relay do outbox precisa de `SELECT ... FOR UPDATE SKIP LOCKED` (SQLite não tem; MySQL 8 tem). O SSE do painel (ADR 0016) precisa acordar o `ms-api` quando o log de eventos recebe linhas, o que no Postgres é `LISTEN/NOTIFY` e no MySQL não tem equivalente nativo. O v1 usava MySQL com Eloquent.

## Decisão

- **PostgreSQL** (versão estável vigente, fixada na Fase 1; o S4 usou a 18) com o driver **`pgx`** (`pgxpool`).
- **`sqlc` não está decidido.** Fica como hipótese para a Fase 2, a primeira que escreve SQL. Critério de decisão: adotar o `sqlc` só se, nas queries reais do outbox e das fotos (`FOR UPDATE SKIP LOCKED`, `= ANY($1)`, `jsonb`, `bigserial`, `RETURNING`), ele gerar código que compile sem contorno, a geração for reproduzível no CI (`git diff --exit-code`) e reduzir o código de `Scan` escrito à mão. Caso contrário, `pgx` direto, sem gerador.
- Sem ORM: o SQL do outbox precisa ficar visível.

## Alternativas

- `database/sql` puro (sem `pgx` nativo): perde `LISTEN/NOTIFY` e o tipo `pgx` para arrays e `jsonb`.
- ORM (GORM e similares): descartado por esconder o SQL do outbox.
- MySQL 8: tem `SKIP LOCKED`, mas não tem `LISTEN/NOTIFY`, e o SSE da ADR 0016 ficaria sem o mecanismo previsto.
- SQLite: não tem `SKIP LOCKED`.

## Custo

Um Postgres a mais para operar localmente. Se o `sqlc` for adotado, a geração de código entra no fluxo de build e no CI.

## Critério de reversão

Revisar o Postgres se o `LISTEN/NOTIFY` se mostrar inadequado ao SSE na Fase 6 (ADR 0016); a alternativa seria polling do log de eventos. Revisar o `pgx` só se um requisito exigir `database/sql` puro. Não é justificativa que o Postgres seja melhor no `testcontainers-go` (há módulo oficial para os dois).

## Evidência

**S4 executado em 2026-11-17** (`specs/spikes/s4-testcontainers.md`): `FOR UPDATE SKIP LOCKED` com `pgx` e Postgres 18 dividiu 1.000 linhas entre duas instâncias do relay (580 e 420) sem perda nem duplicata e pulou as linhas travadas por outra instância. Limites: as instâncias eram goroutines do mesmo processo e a queda foi simulada com rollback. **Não verificado:** `LISTEN/NOTIFY` (fica para a Fase 6) e o `sqlc` (nenhuma comparação com `database/sql` ou `pgx` direto).
