# ADR 0014: Fim de lote e batch.completed

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** N6, Fase 3 (`docs/plan-go.md`)

## Contexto

Com N goroutines, réplicas e redelivery, o fim do lote não tinha dono e "exatamente um log" contradiz *at-least-once*.

## Decisão

Contador atômico no Postgres; um lote termina quando **todas** as fotos chegam a `done` **ou** `failed` (foto na DLQ conta como terminada). Quem finaliza a última foto grava `completed_at` e a linha `batch.completed` no outbox na mesma transação, condicional a `completed_at IS NULL`. O `ms-notification` deduplica por `batch_id` (efeito idempotente, entrega ainda *at-least-once*).

## Alternativas

- Foto na DLQ não contar: lote com id inválido nunca terminaria.
- Detectar fim por consulta periódica (polling).

## Custo

Contagem e `completed_at` na transação do worker aumentam a contenção na linha do lote.

## Critério de reversão

Contenção medida inaceitável na carga da Fase 7.

## Evidência

Pendente. Teste de duas fotos finalizadas ao mesmo tempo (um só evento) e lote com foto inválida.
