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

Cenário de R3 em `specs/oraculo-mensageria.md`; **G1 fechado no lado Go na Fase 4** (ver `specs/fase-4-resiliencia.md`).

**Achado do code-review `high` da Fase 2 (2026-11-18), decidido pelo usuário na Fase 3 (2026-11-18):** o relay do outbox lê `ORDER BY id ... FOR UPDATE SKIP LOCKED`, e um `bigserial` não segue a ordem de commit (a tx com o id 10 pode comitar depois da do 11); com duas instâncias do `ms-ingest`, `SKIP LOCKED` ainda intercala os relays. **Decisão: restringir o FIFO.** O R3 do Go vale como "ordem de **chegada ao broker**", com **um** `ms-ingest`, `WORKER_CONCURRENCY=1` e `prefetch=1`. Sem advisory lock (mantém `SKIP LOCKED` e a escala). **Divergência assumida em relação ao v1**: lá a ordem era a da publicação síncrona; aqui é a da leitura do outbox.

**Evidência da Fase 3** (`specs/execucao-fase-3.log`): `TestWorkerFIFOComConcorrenciaUm` publica 6 fotos em ordem, com concorrência 1 e prefetch 1, e confere que os eventos `photo.done` saem na ordem de `seq` igual à de publicação. O teste **não** cobre a inversão do `bigserial` (transações concorrentes no ingest): ela segue como limitação documentada, não medida. A ADR continua `proposta` até a aprovação do usuário e o fechamento do G1 na Fase 4.
