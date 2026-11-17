# ADRs

Decisões de arquitetura do projeto. Formato: Status, Contexto, Decisão, Alternativas, Custo, Critério de reversão e Evidência.

**Todas estão como `proposta`.** Nenhuma vira `aceita` antes da evidência (spike medido ou teste real) e da aprovação do usuário. O que está em Decisão é hipótese de partida vinda de `docs/plan-go.md`. As ADRs são imutáveis depois de aceitas; mudar de ideia é uma ADR nova que a substitui.

| ADR | Tema | Status | Evidência |
|---|---|---|---|
| [0001](0001-escopo-dominio-fotos.md) | Escopo: domínio de fotos em vez de transações | proposta | pendente |
| [0002](0002-go-e-modulos.md) | Go e estrutura de módulos | proposta | pendente |
| [0003](0003-cliente-amqp-e-reconexao.md) | Cliente AMQP e reconexão | proposta | pendente |
| [0004](0004-entrega-ack-persistencia-confirms.md) | Entrega: ack manual, persistência e publisher confirms | proposta | pendente |
| [0005](0005-retry-degraus-dlq.md) | Retry por degraus, DLQ, Retry-After e jitter | proposta | pendente |
| [0006](0006-idempotencia-do-worker.md) | Idempotência do worker | proposta | pendente |
| [0007](0007-banco-e-acesso-a-dados.md) | Banco e acesso a dados | proposta | pendente |
| [0008](0008-http.md) | HTTP: mux da stdlib ou chi | proposta | pendente |
| [0009](0009-logging-e-observabilidade.md) | Logging e observabilidade | proposta | pendente |
| [0010](0010-fonte-de-fotos-allowlist-ssrf.md) | Fonte de fotos, allowlist e SSRF completa | proposta | pendente |
| [0011](0011-imagem-docker-final.md) | Imagem Docker final, versão do Go e healthcheck | proposta | pendente |
| [0012](0012-outbox-e-idempotency-key.md) | Outbox e Idempotency-Key do POST /batches | proposta | pendente |
| [0013](0013-ordem-de-processamento.md) | Ordem de processamento: FIFO só com concorrência 1 | proposta | pendente |
| [0014](0014-fim-de-lote.md) | Fim de lote e batch.completed | proposta | pendente |
| [0015](0015-platform-e-topology-init.md) | Código compartilhado (platform) e dono único da topologia | proposta | pendente |
| [0016](0016-ms-api-sse-log-de-eventos.md) | ms-api: SSE sobre log de eventos | proposta | pendente |
| [0017](0017-armazenamento-das-miniaturas.md) | Armazenamento das miniaturas | proposta | pendente |
| [0018](0018-painel-cors-auth-e-reprocessamento.md) | Painel: CORS, autenticação do SSE e reprocessamento da DLQ | proposta | pendente |
| [0019](0019-painel-angular.md) | Painel Angular | proposta | pendente |
