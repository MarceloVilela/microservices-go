# ADR 0004: Entrega: ack manual, persistência e publisher confirms

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** D3, RQ5 (`docs/plan-go.md`)

## Contexto

O v1 usava `no_ack=true`, mensagens não persistentes e publicação sem confirmação, então perdia mensagem se o consumidor caísse.

## Decisão

**Ack manual** só após a transação no banco, mensagens **persistentes** em fila durável, **publisher confirms** no relay do outbox.

## Alternativas

- Auto-ack (v1): perde mensagem; descartado.
- Confirms em lote em vez de um por mensagem (menor latência, tratamento mais complexo de falha parcial).
- Quorum queues (sempre duráveis e persistentes); ver ADR 0005.

## Custo

Latência extra por confirm e por ack; entrega passa a ser *at-least-once*, o que exige idempotência (ADR 0006).

## Critério de reversão

Custo de latência inaceitável medido no S1.

## Evidência

Pendente. S1.
