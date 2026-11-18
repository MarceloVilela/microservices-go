# ADR 0013: Ordem de processamento: FIFO só com concorrência 1

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** RQ3, G1 (`docs/plan-go.md`)

## Contexto

O v1 consome em FIFO (R3). Com `prefetch`, N goroutines e reenvio via retry, a ordem não é preservada (a doc do RabbitMQ também não a garante com múltiplos consumers e redelivery).

## Decisão

FIFO **só** com `WORKER_CONCURRENCY=1` e `prefetch=1` (modo oráculo). Com concorrência maior, a ordem não é garantida. O cenário de R3 roda com esses parâmetros declarados.

## Alternativas

- Garantir ordem sempre: derrota o ganho de concorrência.
- Reescrever o cenário de R3 em silêncio: proibido pelo G1.

## Custo

Divergência de comportamento em relação ao v1, registrada aqui e no README.

## Critério de reversão

Se o teste do oráculo mostrar que a ordem falha mesmo com concorrência 1, corrigir o Go ou registrar nova divergência aprovada.

## Evidência

Pendente. Cenário de R3 em `specs/oraculo-mensageria.md` (Fases 3 e 4).
