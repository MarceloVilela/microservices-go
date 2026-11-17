# ADR 0012: Outbox e Idempotency-Key do POST /batches

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** RQ1, RQ2, N1 (`docs/plan-go.md`)

## Contexto

Gravar no Postgres e depois publicar no broker, sem outbox, deixa estado inconsistente numa falha no meio (ex.: 20 de 50 publicados e a API responde 503; repetir o `POST` cria segundo lote).

## Decisão

Tabela `outbox(id, topic, payload, created_at, published_at)` gravada **na mesma transação** do lote; relay com `FOR UPDATE SKIP LOCKED` publica com confirm e só então marca `published_at`. O `POST` responde `202` mesmo com o broker fora (e `/readyz` falha). `Idempotency-Key` repetida devolve o mesmo lote. Publicação *at-least-once*.

## Alternativas

- Publicar direto e compensar em caso de erro.
- Two-phase commit: inviável com RabbitMQ.
- CDC (Debezium e similares): peso desproporcional ao projeto.

## Custo

Mais uma tabela, um relay e possível duplicata na publicação (o worker é idempotente, ADR 0006).

## Critério de reversão

Se esta ADR for rejeitada, a Fase 2 precisa de outra resposta para o `503` parcial.

## Evidência

Pendente. S4 (duas instâncias do relay) e os testes de integração da Fase 2.
