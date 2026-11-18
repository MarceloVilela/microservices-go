# microservices-go

Pipeline de processamento de fotos em microsserviços **Go**, sobre **RabbitMQ 4** e **PostgreSQL**, com painel
**Angular** opcional que mostra a fila ao vivo.

> **Status: Fases 0 a 5 mergeadas; Fase 6 (`ms-api`) implementada, sem PR.** Há monorepo Go, Docker, CI, topologia do broker, `ms-ingest` e `ms-worker`
> (reconexão do consumidor, escada de retry e DLQ inspecionável: `docs/dlq.md`); o `ms-notification` consome `batch.completed` e notifica uma vez por lote (log e e-mail no Mailpit); o `ms-api` lê lotes, fotos e DLQ, serve miniaturas com `ETag`, reprocessa foto da DLQ e transmite eventos por SSE (`Last-Event-ID`); tudo exige `Authorization: Bearer` (token de demonstração, **não é segurança real**). O plano continua sendo um rascunho para aprovação.
>
> Subir: `./run.sh` (RabbitMQ, `topology-init` e os 4 serviços); verificar: `make check`, `make integration`, `make accept`.

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
| 0 | Auditoria do v1, spec, ADRs e spikes | G0 fechado; mergeada |
| 1 | Esqueleto: monorepo, Docker, CI, lint e topologia do broker | mergeada |
| 2 | `ms-ingest`: API, outbox e publicação com confirmação | mergeada |
| 3 | `ms-worker`: consumo, miniatura, idempotência e fim de lote | mergeada |
| 4 | Resiliência: retry, DLQ, reconexão e shutdown | implementada, aceite verde; PR pendente |
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
- [`docs/adr/`](docs/adr): 21 ADRs; 12 aceitas e 9 `proposta` até as fases correspondentes (nenhuma é aceita sem evidência e aprovação).
- [`specs/`](specs): entregáveis das Fases 0 e 1 (auditoria do v1, oráculo, dataset, spikes, esqueleto).

## Metodologia

Spec antes do código, uma ADR por decisão (com critério de reversão), um PR por fase e gates com evidência
versionada. Nenhuma ADR é aceita sem medição e aprovação.

## Limitações assumidas

- Domínio diferente do v1; a paridade é de comportamento.
- Sem deploy real: roda só local, via Docker.
- Retry por degraus de TTL fixo, sem jitter; o *thundering herd* foi medido no S2 e é custo assumido (ADR 0005).
- Token estático de demonstração no painel **não é** segurança real.
