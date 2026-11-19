# microservices-go

Pipeline de processamento de fotos em microsserviços **Go**, sobre **RabbitMQ 4** e **PostgreSQL**, com painel
**Angular** opcional que mostra a fila ao vivo.

> **Status: Fases 0 a 8 mergeadas (PR #1 a #9); Fase 9 (fechamento) em andamento.** Backend completo (`ms-ingest`, `ms-worker`, `ms-notification`, `ms-api`) e painel Angular **opcional** (repositório irmão `microservices-frontend-angular`; aqui entram o serviço `web` em nginx, o CORS e o contrato publicado em `specs/openapi-*.yaml` e `specs/events.schema.json`). O backend fecha sem o painel. Tudo exige `Authorization: Bearer` com token de demonstração, que **não é segurança real**. Sem deploy: roda só local.
>
> Subir: `./run.sh` (RabbitMQ, Postgres, `topology-init` e os 4 serviços; `WITH_WEB=1 ./run.sh` inclui o painel em http://localhost:8080). Verificar: `make check`, `make integration`, `make accept` (`WITH_WEB=1 make accept` inclui o aceite 8), `make load`.

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
| 4 | Resiliência: retry, DLQ, reconexão e shutdown | mergeada |
| 5 | `ms-notification` | mergeada |
| 6 | `ms-api`: leitura, SSE e DLQ | mergeada |
| 7 | Observabilidade, segurança e carga | mergeada |
| 8 | Painel Angular (opcional) | mergeada |
| 9 | Fechamento | em andamento (`specs/fase-9-fechamento.md`) |

O backend (fases 0 a 7) fecha sem o painel.

## Números medidos (G2, Fase 7)

Máquina: Intel i5-8265U, 8 CPUs lógicas, Go 1.27.4. Servidor de imagens **local** (JPEG 1920x1280 com ruído, 640 KB),
Postgres 18 e RabbitMQ 4 em containers, worker nativo. Fonte e comando: [`specs/execucao-fase-7-carga.log`](specs/execucao-fase-7-carga.log), `make load`.
Sem meta inventada: são os números que saíram, em uma máquina e uma rodada.

| Medida | Resultado |
|---|---|
| 500 fotos, `WORKER_CONCURRENCY=1` (FIFO, ADR 0013) | 199 s = **2,5 fotos/s**; pico de memória do worker 32,7 MB |
| 500 fotos, `WORKER_CONCURRENCY=4` | 58 s = **8,6 fotos/s**; pico 67,7 MB |
| 1 lote de 500 × 5 lotes de 100 (conc. 4) | 8,5 × 8,6 fotos/s: **sem contenção mensurável** na linha do lote (ADR 0014) |
| Relay, 2000 linhas, `RELAY_BATCH_SIZE` 100 × 500 | 1035 × 1411 msg/s (sem mudar o padrão: o relay não é o gargalo, o worker é) |
| Escada (ADR 0005): 200 fotos com 2 falhas 503 | as retentativas formam ondas: **pico de 200 req/s** contra mediana de 8 req/s (25x); sem `429` porque o servidor local não limita |
| Imagens finais | ingest 26,8 MB, worker 27 MB, notification 26,7 MB, api 26,8 MB, topology-init 16,2 MB |

Limites: o Picsum real não foi submetido à carga (a rajada de 200 req/s é o risco; ver ADR 0005); uma rodada, sem variância.

## Documentação

- [`docs/plan-go.md`](docs/plan-go.md): plano spec-driven completo (requisitos, gates, arquitetura, fases, riscos).
- [`docs/plan-go-code-review.md`](docs/plan-go-code-review.md): revisão do plano e rastreio das correções.
- [`docs/go101.md`](docs/go101.md): como o projeto foi escolhido.
- [`docs/adr/`](docs/adr): 22 ADRs; 12 aceitas e 10 `proposta` até as fases correspondentes (nenhuma é aceita sem evidência e aprovação).
- [`docs/comparacao-v1-go.md`](docs/comparacao-v1-go.md): v1 (PHP) × Go em linhas, imagem, memória e subida, com o que **não** foi medido.
- [`CHANGELOG.md`](CHANGELOG.md): uma entrada por fase.
- [`specs/`](specs): spec por fase, evidência (`execucao-fase-N.log`), contrato, oráculo e spikes.

## Metodologia

Spec antes do código, uma ADR por decisão (com critério de reversão), um PR por fase e gates com evidência
versionada. Nenhuma ADR é aceita sem medição e aprovação.

## Limitações assumidas

- Domínio diferente do v1; a paridade é de comportamento.
- Sem deploy real: roda só local, via Docker.
- Retry por degraus de TTL fixo, sem jitter; o *thundering herd* foi medido no S2 e é custo assumido (ADR 0005).
- Token estático de demonstração no painel **não é** segurança real.
- Painel opcional: CI do painel ainda não executado (repositório sem git, tag `contract-v1` e segredo `BACKEND_READ_TOKEN` pendentes); sem modo demo offline; sem auditoria de acessibilidade com leitor de tela.
- Carga medida em uma máquina e uma rodada; o Picsum real não foi submetido à rajada da escada de retry (ADR 0005).
- Abertos e rastreados: poda do outbox publicado, reprocesso de linha em quarentena do outbox, foto parada na DLQ (ADR 0022; decidi-la muda o contrato consumido pelo painel, G5, então exige nova versão do contrato), `sqlc` adiado (ADR 0007).
- 10 das 22 ADRs seguem `proposta`, aguardando aprovação.
- Memória e subida do v1 não foram medidas (v1 é oráculo intocado); ver `docs/comparacao-v1-go.md`.
