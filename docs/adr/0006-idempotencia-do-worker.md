# ADR 0006: Idempotência do worker

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** N2, Fase 3 (`docs/plan-go.md`)

## Contexto

A entrega é *at-least-once* (redelivery, duplicata do relay entre confirm e marca), então processar duas vezes a mesma foto não pode duplicar resultado.

## Decisão

Chave `photo_id` + `batch_id`; **fonte da verdade é o banco**. Ordem por mensagem: checar estado (`done`/`failed` ⇒ só ack) → baixar e gerar miniatura em arquivo temporário → `fsync` e `rename` atômico para nome determinístico por `photo_id` → transação (estado, contador, evento, outbox) → ack.

## Alternativas

- Deduplicação por `message_id` em cache (perde estado ao reiniciar).
- Confiar na existência do arquivo (arquivo truncado seria tratado como pronto).

## Custo

Uma leitura no banco por mensagem e escrita em duas etapas (arquivo e banco).

## Critério de reversão

Contenção de lock ou custo medido tornar a checagem inviável; rever a chave ou a ordem.

## Evidência

Pendente. Testes que matam o worker nos três pontos (após o temporário, após o `rename`, após o commit e antes do ack).
