# ADRs

Decisões de arquitetura do projeto. Formato: Status, Contexto, Decisão, Alternativas, Custo, Critério de reversão e Evidência.

**Estado em 2026-11-18:** 12 aceitas (0001, 0002, 0003, 0004, 0005, 0006, 0007, 0010, 0011, 0012, 0014, 0015) e 9 `proposta`. Nenhuma vira `aceita` antes da evidência (spike medido ou teste real) e da aprovação do usuário. Por decisão do usuário, as 10 restantes **ficam `proposta` até as fases correspondentes** (tabela abaixo). O que está em Decisão nas `proposta` é hipótese de partida vinda de `docs/plan-go.md`. As ADRs são imutáveis depois de aceitas; mudar de ideia é uma ADR nova que a substitui.

| ADR `proposta` | Revisar na | Evidência esperada |
|---|---|---|
| 0008 HTTP (stdlib ou `chi`) | Fase 2 | middlewares (CORS, token, logging) escritos; **evidência registrada, aguardando aprovação** |
| 0021 Quarentena do outbox e identidade da mensagem | Fase 3 | evidência registrada; **aguardando aprovação** |
| 0013 Ordem FIFO | Fases 3 e 4 | G1 fechado no lado Go (Fase 4); **aguardando aprovação** |
| 0017 Miniaturas | Fases 3 e 6 | escrita atômica e `ETag`/`304` (Fase 6 verde); **aguardando aprovação** |
| 0020 `Notifier` | Fase 5 | e-mail uma vez no Mailpit; falha de destino isolada; **evidência registrada, aguardando aprovação** |
| 0016 SSE sobre log de eventos | Fase 6 | reconexão com `Last-Event-ID` e `LISTEN/NOTIFY`; **evidência registrada (teste e aceite 6), aguardando aprovação** |
| 0018 CORS, autenticação do SSE e DLQ | **antes** da Fase 6 | escolhas D1 a D3 implementadas e testadas; **evidência registrada, aguardando aprovação** |
| 0009 Logging e observabilidade | Fases 5 e 7 | `slog` com correlação; métricas |
| 0019 Painel Angular | Fase 8 (opcional) | contrato publicado e consumido entre repositórios |

| ADR | Tema | Status | Evidência |
|---|---|---|---|
| [0001](0001-escopo-dominio-fotos.md) | Escopo: domínio de fotos em vez de transações | **aceita** (2026-11-17) | S3 |
| [0002](0002-go-e-modulos.md) | Go e estrutura de módulos | **aceita** (2026-11-18) | Fase 1 |
| [0003](0003-cliente-amqp-e-reconexao.md) | Cliente AMQP e reconexão | **aceita** (2026-11-17) | S1 |
| [0004](0004-entrega-ack-persistencia-confirms.md) | Entrega: ack manual, persistência e publisher confirms | **aceita** (2026-11-17) | S1, oráculo v1 |
| [0005](0005-retry-degraus-dlq.md) | Retry por degraus (escada TTL + DLX), DLQ, Retry-After e jitter | **aceita** (2026-11-17) | S1, S2 |
| [0006](0006-idempotencia-do-worker.md) | Idempotência do worker | **aceita** (2026-11-18) | Fase 3 |
| [0007](0007-banco-e-acesso-a-dados.md) | Banco e acesso a dados | **aceita** (2026-11-18): Postgres + `pgx`; `sqlc` adiado | S4 |
| [0008](0008-http.md) | HTTP: mux da stdlib ou chi | proposta (evidência da Fase 2 registrada; **aguardando aprovação**) | Fase 2 |
| [0009](0009-logging-e-observabilidade.md) | Logging e observabilidade | proposta | pendente |
| [0010](0010-fonte-de-fotos-allowlist-ssrf.md) | Fonte de fotos, allowlist e SSRF completa | **aceita** (2026-11-18) | S3, dataset |
| [0011](0011-imagem-docker-final.md) | Imagem Docker final, versão do Go e healthcheck | **aceita** (2026-11-18) | S5 |
| [0012](0012-outbox-e-idempotency-key.md) | Outbox e Idempotency-Key do POST /batches | **aceita** (2026-11-18) | S4, S1 |
| [0013](0013-ordem-de-processamento.md) | Ordem de processamento: FIFO só com concorrência 1 | proposta (evidência da Fase 3 registrada; **aguardando aprovação**) | Fase 3 |
| [0014](0014-fim-de-lote.md) | Fim de lote e batch.completed | **aceita** (2026-11-18) | Fase 3 |
| [0015](0015-platform-e-topology-init.md) | Código compartilhado (platform) e dono único da topologia | **aceita** (2026-11-18) | Fase 1 |
| [0016](0016-ms-api-sse-log-de-eventos.md) | ms-api: SSE sobre log de eventos | proposta | pendente |
| [0017](0017-armazenamento-das-miniaturas.md) | Armazenamento das miniaturas | proposta (evidência da Fase 3 registrada; **aguardando aprovação**) | Fase 3 |
| [0018](0018-painel-cors-auth-e-reprocessamento.md) | Painel: CORS, autenticação do SSE e reprocessamento da DLQ | proposta | pendente |
| [0019](0019-painel-angular.md) | Painel Angular | proposta | pendente |
| [0020](0020-notificacao-notifier.md) | Notificação: interface Notifier e destinos | proposta | pendente |
| [0021](0021-quarentena-do-outbox-e-identidade-da-mensagem.md) | Quarentena do outbox e identidade da mensagem | proposta (**aguardando aprovação**) | Fase 3 |
