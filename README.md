# microservices-go

Pipeline de processamento de fotos em microsserviços **Go**, sobre **RabbitMQ 4** e **PostgreSQL**, com painel
**Angular** opcional que mostra a fila ao vivo.

> **Status: planejamento.** Não há código ainda, só a documentação em [`docs/`](docs). O plano é um rascunho para
> aprovação; nada abaixo está implementado nem medido.

## O que é

Reimplementação em Go, **inspirada** (não fiel), de um projeto anterior em Lumen/PHP + RabbitMQ + MySQL
(`ec-microservices-rabbitmq`, o v1). O domínio muda de transações para fotos (fonte: Lorem Picsum), então a comparação
com o v1 é de **comportamento de mensageria**, não de dados.

O foco é o que costuma faltar em projetos de estudo com filas:

- publicação confiável: **outbox** transacional no banco e *publisher confirms*;
- consumo seguro: **ack manual**, mensagem persistente, **idempotência** (entrega *at-least-once*);
- falhas: retry por degraus (TTL + DLX), **DLQ** e reprocessamento;
- **reconexão** ao broker e **shutdown gracioso**;
- download seguro de imagens (allowlist, redirect, IP resolvido, limites de bytes e de pixels);
- fim de lote com evento `batch.completed` emitido uma única vez;
- painel em tempo real via **SSE** sobre um log de eventos com `seq`.

## Arquitetura alvo

| Serviço | Papel |
|---|---|
| `ms-ingest` | `POST /batches`: grava lote, fotos e outbox na mesma transação; relay publica `photo.process` |
| `ms-worker` | consome, baixa a imagem, gera a miniatura, atualiza estado e contador do lote |
| `ms-notification` | consome `batch.completed` e registra com `slog` |
| `ms-api` | leitura, SSE (`/events`), miniaturas e reprocessamento da DLQ |
| `ms-queues` | RabbitMQ 4 com painel de gerenciamento |
| `platform/` | módulo compartilhado (config, logging, AMQP com reconexão, tipos de mensagem) |

Serviços não importam uns aos outros. Diagrama e detalhes na seção 6 de [`docs/plan-go.md`](docs/plan-go.md).

## Fases

| Fase | Tema | Status |
|---|---|---|
| 0 | Auditoria do v1, spec, ADRs e spikes | G0 fechado; PR pendente |
| 1 | Esqueleto: monorepo, Docker, CI, lint e topologia do broker | pendente |
| 2 | `ms-ingest`: API, outbox e publicação com confirmação | pendente |
| 3 | `ms-worker`: consumo, miniatura, idempotência e fim de lote | pendente |
| 4 | Resiliência: retry, DLQ, reconexão e shutdown | pendente |
| 5 | `ms-notification` | pendente |
| 6 | `ms-api`: leitura, SSE e DLQ | pendente |
| 7 | Observabilidade, segurança e carga | pendente |
| 8 | Painel Angular (opcional) | pendente |
| 9 | Fechamento | pendente |

O backend (fases 0 a 7) fecha sem o painel.

## Documentação

- [`docs/plan-go.md`](docs/plan-go.md): plano spec-driven completo (requisitos, gates, arquitetura, fases, riscos).
- [`docs/plan-go-code-review.md`](docs/plan-go-code-review.md): revisão do plano e rastreio das correções.
- [`docs/go101.md`](docs/go101.md): como o projeto foi escolhido.
- [`docs/adr/`](docs/adr): 20 ADRs; 8 aceitas e 12 `proposta` até as fases correspondentes (nenhuma é aceita sem evidência e aprovação).
- [`specs/`](specs): entregáveis da Fase 0 (auditoria do v1, oráculo, dataset, spikes).

## Metodologia

Spec antes do código, uma ADR por decisão (com critério de reversão), um PR por fase e gates com evidência
versionada. Nenhuma ADR é aceita sem medição e aprovação.

## Limitações assumidas

- Domínio diferente do v1; a paridade é de comportamento.
- Sem deploy real: roda só local, via Docker.
- Retry por degraus de TTL fixo, sem jitter; o *thundering herd* foi medido no S2 e é custo assumido (ADR 0005).
- Token estático de demonstração no painel **não é** segurança real.
