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

**Achado em aberto, vindo do code-review `high` da Fase 2 (2026-11-18), a decidir antes do teste de R3 na Fase 3:** o relay do outbox lê `ORDER BY id ... FOR UPDATE SKIP LOCKED`, e um `bigserial` não segue a ordem de commit (a tx com o id 10 pode comitar depois da do 11). Com duas instâncias do `ms-ingest`, `SKIP LOCKED` ainda intercala os relays. Resultado: `photo.process` pode receber mensagens fora da ordem de inserção, então o FIFO de R3 não vale nem com `WORKER_CONCURRENCY=1` e `prefetch=1`. Opções a avaliar: relay único com ordem por id (advisory lock, perde escala), aceitar a divergência e restringir o FIFO a "ordem de chegada ao broker", ou ordenar por um critério de commit. Ainda não medido nem decidido.
