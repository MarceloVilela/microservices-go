# ADR 0012: Outbox e Idempotency-Key do POST /batches

- **Status:** aceita (aprovada pelo usuário em 2026-11-18); broker fora do ar e `Idempotency-Key` são critério de aceite da Fase 2
- **Data:** 2026-11-17
- **Origem no plano:** RQ1, RQ2, N1 (`docs/plan-go.md`)

## Contexto

Gravar no Postgres e depois publicar no broker, sem outbox, deixa estado inconsistente numa falha no meio. Exemplo **hipotético** (vem do code review do plano, não foi observado no v1): 20 de 50 publicados e a API responde 503; repetir o `POST` cria segundo lote. No v1, com o broker parado desde o início, a transação **não** é gravada (a conexão abre no construtor, antes do `persist`; ver A2 em `specs/auditoria-v1.md`); a inconsistência existe se o broker cair entre a gravação e a publicação, ou se a publicação falhar depois do `persist`.

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

**S4 executado em 2026-11-17** (`specs/spikes/s4-testcontainers.md`): duas instâncias do relay dividiram 1.000 linhas (580 e 420) sem perda nem duplicata; o relay pulou as 50 linhas travadas por outra instância; a queda entre o confirm e o commit duplica (50 de 200) sem perder nenhuma, o que confirma a publicação *at-least-once*. **S1** mediu o confirm: p50 de 0,42 ms em fila clássica. Limites: as instâncias eram goroutines do mesmo processo e a queda foi simulada com rollback. **Fica para a Fase 2 (critério de aceite):** `POST` responde `202` com o broker parado, as linhas ficam no outbox e `/readyz` falha; ao voltar, o relay publica todas as pendentes; `Idempotency-Key` repetida não cria segundo lote.

**Fase 2 executada em 2026-11-18** (`specs/fase-2-ingest-outbox.md`, `specs/execucao-fase-2.log`):
- Testes de integração (binário real do `ms-ingest`, Postgres 18, RabbitMQ 4): lote, fotos, outbox e evento gravados juntos; `Idempotency-Key` repetida devolve o mesmo lote (`200`, `Idempotent-Replay`), com **20 requisições concorrentes** saiu 1×`202` e 19×`200`, um só lote; mesma chave com outro corpo dá `422`; entrada inválida (`400`) não deixa linha em nenhuma tabela; dois `ms-ingest` (dois relays) publicaram 600 linhas sem perder nenhuma foto.
- **Broker parado:** o `POST` deu `202` em menos de 2 s, as 7 linhas ficaram pendentes (nada marcado sem confirm), o `/readyz` deu `503`; ao voltar, o relay publicou todas e só as do lote certo. O mesmo roteiro passou no aceite de compose (`docker stop ms-queues`).
- Mensagens saem persistentes, com `mandatory` (sem rota vira erro, não perda silenciosa).
- Limites: o broker "parado" nos testes é um proxy TCP que derruba as conexões, não um `docker stop` (esse é o aceite de compose); duplicatas por falha entre confirm e commit não foram provocadas no relay real (só no fake e no S4); polling de 500 ms, sem `LISTEN/NOTIFY`.
