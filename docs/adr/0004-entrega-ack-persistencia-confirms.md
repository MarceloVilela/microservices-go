# ADR 0004: Entrega: ack manual, persistência e publisher confirms

- **Status:** aceita (aprovada pelo usuário em 2026-11-17)
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

Parcial. **S1 executado em 2026-11-17** (`specs/spikes/s1-amqp.md`): 1.000 publicações com confirm: p50 430 µs, p95 796 µs, p99 1,7 ms (sequencial); em lote ~31 mil/s. Critério de reversão (latência) não acionado. Mensagens persistentes sobreviveram ao reinício. O oráculo do v1 (`specs/oraculo-v1-resultado.txt`) confirmou as falhas que esta ADR corrige: 2 mensagens perdidas após `docker restart` do broker (O4) e 3 perdidas com consumidor derrubado e `no_ack=true` (O5). **Não cobre** quorum queues (latência de confirm não medida) nem o ack manual do consumidor; ambos ficam como critério de aceite (ack manual na Fase 3; quorum só se a ADR 0005 mudar de mecanismo). Medido depois para a ADR 0005: em quorum o confirm tem p50 de 5,3 ms (12× a clássica) e ~173/s sequencial; a ADR 0005 manteve a fila clássica.
