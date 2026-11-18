# Plano de Implementação (Spec-Driven Design): pipeline de fotos em Go sobre RabbitMQ, com painel Angular

> Reescrita, em **Go**, do `ec-microservices-rabbitmq` (v1: Lumen/PHP 8.4 + RabbitMQ 4 + MySQL, três
> serviços em Docker). O domínio muda de **transações** para **processamento de fotos** (fonte: Lorem Picsum),
> então este projeto é uma **reimplementação inspirada no v1**, não uma reescrita fiel. O que continua
> sendo oráculo é o **comportamento de mensageria** (R2, R3 e R5 do v1).
>
> O projeto ganha um **painel Angular** (fase opcional) que torna a fila visível: lote ao vivo, galeria de
> miniaturas e fila de falhas (DLQ) com reprocessamento. Ele consome um serviço novo, o `ms-api`, e **não**
> o backend do `devfinder` (domínios sem relação; ver `projects-ia/microservices-frontend-angular/docs/angular101.md`, antigo `zangular.md`).
>
> Metodologia: a mesma do `php-laravel/plan.md` (spec antes do código, ADR com critério de reversão,
> um PR por fase, gates com evidência versionada).
>
> **Este plano é um rascunho para aprovação.** Nada foi implementado. Os itens marcados *(hipótese)*
> viram ADR na Fase 0, com evidência medida, antes de virar decisão.
> **Revisado** em 2026-11-17 pelo code review `high` em `plan-go-code-review.md` (11 achados, todos tratados; ver
> a tabela de rastreio no fim daquele arquivo).

## Status

| Fase | Nome | Tamanho | Status |
|---|---|---|---|
| 0 | Auditoria do v1, spec, ADRs e spikes | M | **G0 fechado em 2026-11-18**; PR #1 mergeado |
| 1 | Esqueleto: monorepo Go, Docker, CI, lint e topologia do broker | M | **mergeada** (PR #2); `specs/fase-1-esqueleto.md` |
| 2 | `ms-ingest`: API HTTP, outbox e publicação com confirmação | M | **mergeada** (PR #3); `specs/fase-2-ingest-outbox.md` |
| 3 | `ms-worker`: consumo, download, miniatura, ack manual, idempotência e fim de lote | L | **mergeada** (PR #4); `specs/fase-3-worker.md` |
| 4 | Resiliência: retry com backoff, DLQ, reconexão e shutdown gracioso | L | **mergeada** (PR #5); `specs/fase-4-resiliencia.md` |
| 5 | `ms-notification`: evento de fim de lote e abstração `Notifier` (log; e-mail opcional) | S | **implementada em 2026-11-18, aceite verde**; PR pendente (a pedido); `specs/fase-5-notification.md` |
| 6 | `ms-api`: leitura, stream de eventos (SSE) e reprocessamento da DLQ | M | pendente |
| 7 | Observabilidade, segurança e verificação de carga | M | pendente |
| 8 | Painel Angular (**opcional**) | L | pendente |
| 9 | Fechamento (long tail, README honesto, comparação com o v1) | S | pendente |

Os tamanhos são estimativas relativas, não prazos. O backend Go (fases 0 a 7) é o centro do projeto e fecha
sem o painel; a Fase 8 só começa com o PR da Fase 7 mergeado.

## 1. Ponto de partida: o que o v1 realmente é

O v1 **já está implementado** (etapas 1 a 9 do `specs.md`, três commits em `main`). Só o `specs.md` descreve
"nada implementado"; o `CLAUDE.local.md` do v1 está atualizado.

| Item | Estado no v1 (`projects-ia/ec-microservices-rabbitmq`) |
|---|---|
| Serviços | `ms-transaction` (publica), `ms-queues` (RabbitMQ 4), `ms-notification` (consome) |
| Docs | `specs.md` (R1 a R8), `docs/prompts-desenvolvimento`, `docs/saidas-claude`; **`docs/adr/` vazio** |
| Testes | só `ExampleTest` do Lumen: **sem teste real** |
| Pendências conhecidas (code review do v1) | conexão no construtor (broker parado dá 500 até com body inválido); falha do Loggly derruba `/notification/send`; consumidor com `no_ack=true`, mensagens não persistentes e sem reconexão |

As três pendências do v1 viram **requisitos de primeira classe** no Go (ver seção 3), em vez de limitações.

## 2. Decisões que mudam o v1 (D1 a D6)

| # | Decisão (Go) | v1 | Por que mudar | O que invalidaria |
|---|---|---|---|---|
| D1 | **Go** (versão estável vigente, fixada na ADR), um módulo por serviço com `go.work` e um módulo `platform` compartilhado (**ADR 0002 e 0015, aceitas em 2026-11-18**, Go 1.27.4 fixado na Fase 1) | PHP 8.4 + Lumen 10 (descontinuado) | Lumen está em fim de vida; Go é a stack comum para consumers concorrentes e binários pequenos | Spike mostrar fricção que não compense |
| D2 | **Cliente `amqp091-go`** com camada própria de reconexão | `php-amqplib`, sem reconexão | Biblioteca oficial do RabbitMQ para Go; a reconexão é responsabilidade do chamador, então precisa de código e de teste | Biblioteca descontinuada ou bug bloqueante no spike |
| D3 | **Ack manual**, mensagem **persistente**, **publisher confirms** | `no_ack=true`, não persistente, sem confirmação | Corrige as pendências do v1; é o que torna R5 verdadeiro com o broker reiniciando | Custo de latência inaceitável no spike |
| D4 | **Retry por degraus**: uma fila de retry por degrau (5 s, 30 s, 2 min, 5 min), cada uma com **TTL fixo** (sem bloqueio de cabeça de fila) e DLX de volta; depois **DLQ**. `Retry-After` é arredondado **para cima** ao degrau mais próximo. **Sem jitter** nesse mecanismo (assumido; herd medido no S2). **Decidido pelo usuário em 2026-11-17** (ADR 0005), comparando com o retry nativo da quorum queue | sem retry | Escada exata (5,003 s e 30,004 s medidos), `Retry-After` por degrau, fila clássica (confirm p50 0,42 ms) | O herd causar `429` no Picsum na carga da Fase 7, ou a escada ficar cara de manter: ADR 0005 troca pelo retry nativo da quorum queue (S2) ou por retry no Postgres (`next_attempt_at` com jitter, aproveitando o relay do outbox) |
| D5 | **PostgreSQL** + `pgx` + `sqlc` *(hipótese)* | MySQL 8.4 + Eloquent | SQL tipado e gerado, sem ORM; `SELECT ... FOR UPDATE SKIP LOCKED` para o relay do outbox (SQLite não tem) | `database/sql` puro bastar para o volume do projeto. **Não** é justificativa que Postgres seja melhor no `testcontainers-go`: há módulo oficial para os dois, e essa afirmação saiu do plano |
| D6 | **Estrutura por serviço**: `cmd/`, `internal/{domain,app,adapters}`; broker atrás de uma interface pequena definida **no consumidor** | `BrokerInterface` + provider | Idiomático em Go; interface no lado de quem usa, não de quem implementa | Cerimônia sem valor. **Revisitado na Fase 1 (F1-7):** `internal/{domain,app,adapters}` só nasce com o primeiro código (Fase 2); a Fase 1 tem só `cmd/` |

**Sobre o retry (D4, decidido em 2026-11-17)**: o S2 (`specs/spikes/s2-retry.md`) mediu a escada TTL + DLX
(5,003 s e 30,004 s) e o retry nativo da quorum queue (linear, `min × contagem`). O usuário escolheu a **escada**; a
tabela comparativa está na ADR 0005. Custos assumidos:

- **Sem jitter**: jobs que falharam juntos voltam juntos. Medido no S2: **105 de 200 reentregas em 100 ms**.
- **Uma fila por degrau, com TTL na fila**: TTL por mensagem em fila única bloqueia (a de 1 s esperou a de 6 s).
- O retry nativo fica como alternativa documentada e medida (ADR 0005), com as armadilhas conhecidas: `nack` com
  requeue não conta, o broker manda à DLQ sozinho no `x-delivery-limit` e quorum custa 12× mais por confirm.

**Custo assumido às claras**: trocar o domínio quebra o oráculo de dados do v1 (10 usuários, 50 transações).
O dataset de paridade passa a ser de **fotos** (ver Fase 0). Isso exige a ADR 0001, de escopo.

## 3. Requisitos (mapeamento v1 → Go)

Os requisitos do Go usam o prefixo **RQ** (e **N** para os novos). **G0 a G5** são só os gates da seção 5; os dois
conjuntos de ids não se misturam.

| v1 | Go | Mudança |
|---|---|---|
| R1 `POST /transaction` persiste e responde | **RQ1** `POST /batches` cria um lote e N jobs de foto, grava tudo **e a intenção de publicar (outbox) na mesma transação** e responde `202`. Aceita `Idempotency-Key` | domínio novo; outbox |
| R2 publica `{transaction,name,email,value}` na fila sem esperar resposta | **RQ2** um relay publica um job `photo.process` por foto, **com confirmação do broker**, sem esperar o worker | confirms; mensagem persistente |
| R3 consumidor FIFO | **RQ3** `ms-worker` consome com `prefetch` e N goroutines. **A ordem FIFO só vale com `WORKER_CONCURRENCY=1` e `prefetch=1`** (modo oráculo); com concorrência maior a ordem não é garantida | divergência assumida (ADR 0013) |
| R4 `POST /notification/send` grava no Loggly | **RQ4** evento `batch.completed` consumido por `ms-notification`, que notifica por uma abstração `Notifier` (padrão: `slog`; opcional: e-mail via SMTP) | Loggly **descartado** (ADR 0009); falha de destino **não derruba o fluxo** |
| R5 mensagens ficam em *Ready* se o consumidor cai | **RQ5** vale também com **broker reiniciando** (fila durável + mensagem persistente + reconexão) | mais forte que o v1 |
| R6 seed de 10 usuários e 50 transações | **RQ6** dataset de paridade: ids de fotos fixos do Picsum, lote determinístico | domínio novo |
| R7 `client.http` | **RQ7** `specs/acceptance/*.http` | igual |
| R8 broker atrás de interface | **RQ8** interface mínima no consumidor, fake nos testes de unidade | igual, idiomático |

Requisitos novos (sem equivalente no v1):

- **N1 Retry e DLQ**: falha **transitória** (timeout, 5xx, 429) tenta de novo por degraus; falha **definitiva** (404, imagem inválida, **host fora da allowlist**, dimensões acima do teto) vai direto para a DLQ; esgotados os degraus, DLQ.
- **N2 Idempotência**: a entrega é *at-least-once*, então processar a mesma foto duas vezes não duplica resultado (chave: `photo_id` + `batch_id`; fonte da verdade é o **banco**, ver ordem de operações na Fase 3).
- **N3 Shutdown gracioso**: ao receber SIGTERM, o worker para de consumir, termina o que está em andamento e só então sai.
- **N4 Segurança do download** (completa; ver Fase 7): allowlist de hosts **no ingest (400) e no worker**; `CheckRedirect` reaplicando a allowlist; checagem do **IP resolvido** (bloqueia loopback, link-local e redes privadas, contra DNS rebinding); timeout; limite de bytes; **teto de pixels via `image.DecodeConfig` antes de decodificar**.
- **N5 Saúde**: `/healthz` (processo) e `/readyz` (broker e banco conectados) em cada serviço, e um subcomando `healthcheck` no próprio binário (a imagem final não tem `curl`).
- **N6 Fim de lote**: um lote termina quando **todas** as suas fotos chegam a um estado final, `done` **ou** `failed` (foto na DLQ conta como terminada). O evento `batch.completed` sai **uma vez por lote** (ver Fase 3); o consumo é idempotente por `batch_id`.
- **N7 Painel** (Fase 8, opcional): o `ms-api` expõe leitura, SSE e reprocessamento; o frontend nunca fala direto com o broker nem com o banco. O painel também **cria lotes** (`POST /batches` no `ms-ingest`, com CORS e o mesmo token) e tem um modo "lote com falhas" (dataset com ids inválidos) para exercitar retry e DLQ sem `curl`.
- **N8 Eventos para o painel**: todo estado visível ao frontend vem de um **log de eventos** com `seq` (seção 6), nunca de polling do estado nem de mensagens do broker. O esquema dos eventos é versionado em `specs/events.schema.json`.
- **N9 Contrato amigável ao frontend**: listagens com **paginação por cursor**, filtro por `status` e `batch_id` e ordenação estável (`GET /jobs` do `angular101.md`, antigo `zangular.md`, é `GET /photos?status=`); erros em formato único (`application/problem+json`) com `code` estável para o painel mapear mensagens; `Cache-Control`/`ETag` nas miniaturas.

## 4. Lições herdadas (gates, não recomendações)

| Lição | Origem | Gate neste plano |
|---|---|---|
| Conexão com o broker no construtor deixa o serviço refém dele (500 até com body inválido) | v1, code review | Validar a entrada **antes** de tocar em qualquer dependência; com o **outbox**, o `ms-ingest` aceita o lote (`202`) mesmo com o broker fora do ar, e `/readyz` falha para sinalizar |
| Falha de um sistema periférico (Loggly) derrubou a rota principal | v1, code review | Notificação desacoplada por evento; falha do destino de log nunca propaga ao fluxo principal |
| `no_ack` perde mensagem se o consumidor cai | v1, code review | D3: ack manual, teste que mata o worker em **cada** ponto de falha da Fase 3 e confere a reentrega |
| Conferir `echo $?`, **sem pipe**: ferramenta "parecia verde" e saía com código 1 | `php-codei`, `reactjs` | Todo gate declara o código de saída esperado; scripts com `set -euo pipefail`; `go test` e `golangci-lint` sempre com `echo $?` |
| Reproduzir o CI exato localmente: o `.env` local tinha mais chaves que o do workflow | `php-codei` | Reproduzir o workflow em container limpo antes de todo PR que toque CI, `go.mod`/`go.sum`, `Dockerfile` ou `.env*` |
| Variável de ambiente com nome inválido sumiu em silêncio | `php-codei` | Só `[A-Z0-9_]`; comando `config check` valida a **lista completa** de variáveis no CI e no boot; log no boot com os *nomes* (nunca os valores) |
| Retry curto demais esgota tentativas contra um limite de 1 minuto | `transcript` | Degraus com **teto de 5 min**; `Retry-After` arredondado para cima ao degrau; falha definitiva **registrada** com causa. Jitter só se a ADR 0005 trocar o mecanismo (D4) |
| Imagem Docker que nasce com tamanho/versão surpresa, ou sem o que o processo precisa | geral | Imagem final **`distroless/static`** (tem certificados CA; `scratch` só se a ADR 0011 copiar `ca-certificates` e o teste de download HTTPS passar); tamanho medido; **teste de download HTTPS dentro da imagem final já na Fase 1** |
| Segredo em arquivo versionado | `fyoker-tier.md` | `govulncheck` e varredura de segredos no CI; `.env.example` sem valores |
| Doc apontando para arquivo renomeado | `php-codei` | Fase 0: toda referência cruzada conferida contra o arquivo real |
| Requisito e gate com o mesmo id (G1 a G8 × G0 a G4) deixaram "fechado" ambíguo | code review do plano | Requisitos são `RQ`, gates são `G`; toda referência usa o prefixo completo |

## 5. Metodologia e definição de pronto

Cada fase: **spec** (`specs/fase-N-nome.md`) → **branch** `fase-N-nome` a partir de `main` → **teste** antes
do código → **verificar** os gates, guardando a saída em `specs/execucao-fase-N.log` → **PR** só quando o
critério de aceite já bate e **só quando o usuário pedir** → merge e só então a fase seguinte.
Commit, push e PR são decisão humana, sempre. Sem co-autor do Claude em commits e PRs.

Definição de pronto (toda fase com código):

- [ ] Spec aprovada e ADRs novas escritas, cada uma com **Critério de reversão**.
- [ ] `go build ./...`, `go vet ./...` e `go test -race ./...` com código de saída `0`.
- [ ] `golangci-lint run` e `govulncheck ./...` com código de saída `0`.
- [ ] Teste de arquitetura verde (gate G4).
- [ ] CI reproduzido localmente, em container limpo.
- [ ] Nenhum `panic` fora de `main`; nenhum erro engolido; `context.Context` propagado até o broker e o banco.
- [ ] Evidência real versionada (saída dos `.http`, medições).
- [ ] Sem segredo no diff.

### Gates

| Gate | Quando | Critério (metas a calibrar no spike) | Se falhar |
|---|---|---|---|
| **G0** Viabilidade | fim da Fase 0 | S1 a S4 com resultado medido | Replanejar D1 a D6 antes de escrever domínio |
| **G1** Paridade de mensageria | Fases 3 e 4 | Os cenários de R2, R3 e R5 do v1 passam também no Go (ver `specs/oraculo-mensageria.md`). **O cenário de R3 roda com `WORKER_CONCURRENCY=1` e `prefetch=1`**; a divergência de ordem com concorrência maior está na ADR 0013 | Corrigir o Go, ou registrar divergência como ADR **aprovada** (nunca reescrever o cenário em silêncio) |
| **G2** Eficiência | Fases 3 e 7 | Imagem final e memória do worker medidas e registradas; **sem meta inventada**, só número real | Investigar antes de continuar |
| **G3** Qualidade | toda fase | `-race`, lint e `govulncheck` com `0` | Corrigir antes do PR |
| **G4** Arquitetura | toda fase com código | Regra de dependência (`internal/domain` não importa `adapters`; serviços só dependem de `platform`, nunca uns dos outros) verificada por teste ou `depguard` | Corrigir; mudar a regra só por ADR |
| **G5** Contrato | Fases 6 e 8 | OpenAPI do `ms-api` e do `ms-ingest` e `events.schema.json` validados no CI; cliente e tipos TypeScript do painel (repositório separado) **gerados** a partir do contrato publicado aqui, sem diferença (`git diff --exit-code` no repositório do painel) | Corrigir o contrato primeiro, depois o código |

## 6. Arquitetura alvo (hipótese a confirmar nos spikes)

```
cliente ──POST /batches (Idempotency-Key)──▶ ms-ingest ──tx única──▶ Postgres (batches, photos, outbox)
                                                 │
                                                 └─ relay do outbox (SKIP LOCKED) publica photo.process
                                                    (persistente, com confirm) ─▶ exchange `photos` (direct)
                                                                                      │
                                                    queue photo.process  ◀─ ms-worker (N goroutines, ack manual)
                                                      │ falha transitória
                                                      ▼
                                    photo.process.retry.{5s,30s,2m,5m} (TTL fixo + DLX de volta)
                                                      │ esgotou / falha definitiva
                                                      ▼
                                             photo.process.dlq

ms-worker ── tx: status da foto + contador do lote (+ linha no outbox quando fecha) ──▶ Postgres
relay ─▶ queue batch.completed ─▶ ms-notification (Notifier: slog, e-mail opcional; dedup por batch_id)

worker/ms-api ── na mesma tx de cada mudança de estado ──▶ Postgres (tabela events, seq crescente) + NOTIFY
ms-api ──leitura──▶ Postgres        ms-api ──LISTEN + eventos com seq > Last-Event-ID──▶ SSE (/events) ──▶ painel Angular (Fase 8)
ms-api ──POST /dlq/{id}/retry──▶ marca a foto como pending + nova linha no outbox + evento
painel Angular ──POST /batches (CORS + token)──▶ ms-ingest        painel ──<img src=/photos/{id}/thumb>──▶ ms-api
ms-queues = RabbitMQ 4 management (rede externa compartilhada, como no v1)
```

- **Topologia como código, com dono único**: um **comando `topology-init`** (uma execução, no boot do `ms-queues`) declara exchange, filas, degraus de retry, DLQ e `batch.completed`. Os demais serviços só **verificam** a topologia (`QueueDeclarePassive`) e falham o `/readyz` se ela não bater. Isso evita `PRECONDITION_FAILED` por argumentos divergentes entre versões de imagem.
- **Código compartilhado**: só o módulo `platform` (config, `slog`, conexão AMQP com reconexão, tipos das mensagens). Regra e ADR 0015; serviços não importam uns aos outros (G4).
- **Mensagem**: JSON versionado (`{"v":1,"batch_id":...,"photo_id":...,"source_url":...}`), com `message_id` e `correlation_id`.
- **Outbox**: tabela `outbox(id, routing_key, payload, created_at, published_at, attempts, last_error, parked_at)`; o relay seleciona com `FOR UPDATE SKIP LOCKED`, publica com confirm e só então marca `published_at`. Publicação *at-least-once* (pode duplicar após falha entre confirm e marca; o worker é idempotente, N2).
- **Log de eventos (fonte do SSE)**: tabela `events(seq bigserial, type, batch_id, photo_id, payload, created_at)`, escrita **na mesma transação** de cada mudança de estado (worker, retry da DLQ, criação de lote). O `ms-api` acorda por `LISTEN/NOTIFY` e lê `seq > Last-Event-ID`; o `seq` é o `id` do evento SSE, então reconectar não perde nem repete evento. Retenção limitada (ex.: últimas 24 h ou N linhas), configurável. Sem isso o `Last-Event-ID` não teria o que reler.
- **Armazenamento**: metadados no Postgres; miniatura em volume local (`/data/thumbs`), servida pelo `ms-api` (`GET /photos/{id}/thumb`). MinIO fica como alternativa na ADR 0017.
- **Miniatura**: `disintegration/imaging` ou `image/draw` da stdlib *(hipótese; spike S3 decide)*.

## 7. Fases

### Fase 0: Auditoria do v1, spec, ADRs e spikes

Entregáveis em `specs/`:

1. **`auditoria-v1.md`**: lê o código do v1 (não só o `specs.md`) e lista achados, incluindo as 3 pendências do code review e o que mais aparecer. Cada achado com decisão (herdar, corrigir, descartar).
2. **`oraculo-mensageria.md`**: cenários observáveis do v1 que o Go precisa reproduzir (publicar sem esperar; fila em *Ready* com consumidor parado; entrega ao voltar; FIFO). Cada cenário é um roteiro executável contra o v1 e contra o Go, **com os parâmetros do Go declarados** (ex.: R3 com concorrência 1).
3. **`dataset-de-paridade.md`**: lote determinístico de fotos (ids fixos do Picsum), tamanhos esperados de miniatura e o resultado esperado por foto, incluindo ids propositalmente inválidos (404), uma URL fora da allowlist e uma imagem acima do teto de pixels, para exercitar retry, DLQ direta e fim de lote com falha.
4. **ADRs** em `docs/adr/` (formato do `php-laravel`: Status, Contexto, Decisão, Alternativas, Custo, Critério de reversão). **Nenhuma vira `aceita` antes da evidência e da sua aprovação.**

   | ADR | Tema |
   |---|---|
   | 0001 | Escopo: domínio de fotos em vez de transações |
   | 0002 | Go e estrutura de módulos |
   | 0003 | Cliente AMQP e reconexão |
   | 0004 | Entrega: ack manual, persistência e publisher confirms |
   | 0005 | Retry por degraus (TTL fixo + DLX), DLQ e `Retry-After`: **decidido** (escada) contra o retry nativo das quorum queues; herd e falta de jitter como custos assumidos |
   | 0006 | Idempotência do worker |
   | 0007 | Banco e acesso a dados (`pgx` + `sqlc` ou `database/sql`) |
   | 0008 | HTTP (`net/http` com mux da stdlib ou `chi`) |
   | 0009 | Logging e observabilidade (`slog`; Loggly descartado) |
   | 0010 | Fonte de fotos, allowlist de hosts e SSRF completa (redirect, IP resolvido, teto de pixels) |
   | 0011 | Imagem Docker final (`distroless/static` ou `scratch` com CA certs), versão do Go e healthcheck |
   | 0012 | **Outbox** e `Idempotency-Key` do `POST /batches` (consistência banco × fila) |
   | 0013 | **Ordem de processamento**: FIFO só com concorrência 1; divergência de R3 |
   | 0014 | **Fim de lote**: contador atômico, foto na DLQ conta como terminada, `batch.completed` via outbox |
   | 0015 | Código compartilhado (`platform`) e dono único da topologia (`topology-init`) |
   | 0016 | `ms-api`: tempo real com **SSE** sobre o **log de eventos** (`seq` + `LISTEN/NOTIFY`), retenção, *heartbeat* e cliente lento (alternativas: WebSocket, polling) |
   | 0017 | Armazenamento das miniaturas: volume local ou MinIO; cache (`ETag`) e acesso pelo `<img>` |
   | 0018 | Painel: CORS (ingest e `ms-api`), **autenticação do SSE** (o `EventSource` nativo não envia `Authorization`), acesso às miniaturas e semântica do reprocessamento da DLQ |
   | 0019 | Painel Angular em **repositório separado**: versão, estado (signals × NgRx), cliente SSE, geração do cliente TypeScript a partir do contrato publicado (G5 entre repositórios) e estratégia de testes |
   | 0020 | Notificação: interface `Notifier` no consumidor, destinos (`slog` padrão, SMTP opcional com Mailpit no desenvolvimento), idempotência por `batch_id` e isolamento de falha |

5. **Spikes** (resultado medido em `specs/spikes/`):

   | Spike | Pergunta | Evidência |
   |---|---|---|
   | S1 | `amqp091-go`: publisher confirms e reconexão funcionam com RabbitMQ 4? Quanto custa a latência? | número medido, 1.000 publicações |
   | S2 | *(executado em 2026-11-17)* Retry por degraus com TTL fixo + DLX se comporta como esperado (ordem, backoff, DLQ)? Há *thundering herd* mensurável? O retry atrasado nativo das quorum queues está disponível na versão do broker usada e faz melhor? | log do painel e do teste |
   | S3 | **Lorem Picsum**: `/v2/list`, redimensionamento, redirects, limites e erro de id inexistente (**a API foi recomendada de memória; confirmar contrato e comportamento antes de aceitar a ADR 0010**) | respostas reais versionadas |
   | S4 | `testcontainers-go` com RabbitMQ 4 e Postgres roda no CI sem segredo? O relay com `SKIP LOCKED` se comporta com duas instâncias? | execução no CI local |
   | S5 | Imagem final: download HTTPS e `healthcheck` funcionam dentro dela? | execução em container |

6. Referências cruzadas do `CLAUDE.md` e do `specs/README.md` conferidas contra os arquivos reais (feito em 2026-11-17; ver `specs/execucao-fase-0.log`).

**Aceite**: auditoria, oráculo e dataset escritos; ADRs 0001 a 0020 escritas (as com evidência, aceitas); S1 a S5 com número; **G0** fechado.

### Fase 1: Esqueleto

- Monorepo (`ms-ingest`, `ms-worker`, `ms-notification`, `ms-api` e `platform/`), `Makefile`, `docker-compose` por serviço mais rede externa compartilhada, `run.sh` e `stop.sh` equivalentes ao v1.
- Comando `topology-init` declarando exchange, filas, degraus de retry, DLQ e `batch.completed`; verificação passiva nos demais serviços; `config check` (lista completa de variáveis).
- CI: `go build`, `go vet`, `go test -race`, `golangci-lint`, `govulncheck`, build das imagens **e teste de download HTTPS dentro da imagem final**.
- `/healthz`, `/readyz` e subcomando `healthcheck` nos serviços.

**Aceite**: `docker compose up` sobe tudo; painel em `localhost:15672` mostra a topologia criada **só** pelo `topology-init`; um serviço com topologia divergente falha o `/readyz` em vez de recriá-la; download HTTPS funciona na imagem final; CI verde reproduzido localmente (`echo $?` = `0`).

### Fase 2: `ms-ingest`

- `POST /batches` valida a entrada **antes** de tocar em qualquer dependência (host fora da allowlist e `source_url` inválida dão `400`), grava lote, fotos e **linhas do outbox na mesma transação** e responde `202` com o `batch_id`. `Idempotency-Key` repetida devolve o mesmo lote.
- Relay do outbox publica com confirmação (`SKIP LOCKED`, suporta mais de uma instância) e marca `published_at` só depois do confirm.
- CORS com allowlist de origens e o mesmo token de demonstração do `ms-api` (middleware em `platform`), para o painel criar lotes (N7); grava o evento de criação do lote em `events`.
- Teste de integração com `testcontainers-go`; `.http` de aceite; contrato OpenAPI curto em `specs/`.

**Aceite**: lote publicado aparece como *Ready*; com o broker parado o `POST` ainda dá `202`, as linhas ficam no outbox e `/readyz` falha; ao voltar, o relay publica **todas** as pendentes (nenhuma perdida, nenhuma fora do lote); corpo inválido dá `400`; mesmo `Idempotency-Key` não cria segundo lote; **RQ2** (publicar sem esperar) coberto.

### Fase 3: `ms-worker`

- Consumo com `prefetch` e N goroutines (`WORKER_CONCURRENCY`).
- **Ordem de operações por mensagem** (ADR 0006 e 0014):
  1. checa o estado da foto no banco (`done`/`failed` ⇒ só dá ack);
  2. baixa (allowlist, redirect, IP, timeout, bytes, teto de pixels) e gera a miniatura **num arquivo temporário**;
  3. `fsync` e `rename` atômico para o nome definitivo (determinístico por `photo_id`);
  4. **uma transação**: marca a foto como `done` (ou `failed`), incrementa o contador do lote e, se este for o último, grava `completed_at` e a linha `batch.completed` no outbox (condicional a `completed_at IS NULL`, então sai uma vez); **na mesma transação grava a linha em `events`** (estado da foto e, se for o caso, fim do lote);
  5. só então **ack**.
- Falha transitória vai para o degrau de retry; falha definitiva vai direto à DLQ **e** conta como terminada (passo 4 com `failed`).
- Testes de unidade com fake do broker (interface no consumidor, RQ8).
- **Pendências da Fase 2, resolvidas na Fase 3 (2026-11-18):** ordem do outbox (FIFO restrito à ordem de chegada ao broker, ADR 0013), quarentena de linha venenosa no relay e identidade da mensagem (`message_id`; coluna `topic` removida), ver `specs/fase-3-worker.md`.

**Aceite**: lote do dataset processado e miniaturas conferidas contra o esperado; **matar o worker em cada um de três pontos** (após o arquivo temporário, após o `rename`, após o commit e antes do ack) reentrega a mensagem sem duplicar resultado nem deixar miniatura truncada; um lote com uma foto inválida **termina** e emite `batch.completed` uma vez; duas fotos finalizadas ao mesmo tempo não geram dois eventos; **G1** verde nos cenários de R3 (com concorrência 1) e R2.

### Fase 4: Resiliência

> **Status (2026-11-18): implementada na branch `fase-4-resiliencia`, aceite verde, sem PR.** Spec e desvios: `specs/fase-4-resiliencia.md`; evidência: `specs/execucao-fase-4.log`. D1 (infra consome degrau) e D2 (reler estado antes da DLQ) resolvidas; `Requeue` sem limite eliminado.

- Retry por degraus, falha definitiva direto à DLQ, DLQ com inspeção documentada.
- **Herdado da Fase 3 (code-review), resolvido:** erro de banco ou disco consome degrau (classe `infrastructure`; esgotado vai à DLQ e a foto fica `pending`); DLQ × transação: relê o estado antes da DLQ e aceita a janela residual (ver `specs/fase-4-resiliencia.md`).
- Reconexão do worker, do ingest e do relay com backoff; shutdown gracioso (N3).
- Cenários do oráculo: consumidor parado (R5 do v1) **e** broker reiniciado (mais forte que o v1).

**Aceite**: roteiros de `oraculo-mensageria.md` passam contra o Go; id inválido do dataset termina na DLQ com a causa registrada; `Retry-After` de 45 s espera o degrau de 2 min (arredondado para cima); **G1** fechado.

### Fase 5: `ms-notification`

> **Status (2026-11-18): implementada na branch `fase-5-notification`, aceite verde, sem PR.** Spec e decisões: `specs/fase-5-notification.md` (D1: dedup em tabela `notifications` no Postgres; D2: falha de destino é registrada e a mensagem recebe ack). Evidência: `specs/execucao-fase-5.log`. O `Supervisor` foi para `platform/amqpx`. Itens do code-review registrados (sem reprocesso de e-mail com `last_error`, consumidor duplicado com o worker, migração do notification feita pelo ingest): ver `specs/fase-5-notification.md`.

- Consome `batch.completed` e notifica por uma interface pequena `Notifier`, **definida no consumidor** (ADR 0020), deduplicando por `batch_id`. Destino padrão: `slog` (JSON). Destino opcional: **e-mail via SMTP** (Mailpit em container no desenvolvimento, sem envio real). Nenhum destino **nunca** derruba o consumo.

**Aceite**: reentregar o mesmo `batch.completed` duas vezes gera **um** log (efeito idempotente; a entrega continua *at-least-once*); com o destino indisponível (SMTP fora do ar, por exemplo) o fluxo continua e a falha fica registrada; o e-mail chega **uma vez** no Mailpit.

### Fase 6: `ms-api`

- Leitura (N9): `GET /batches`, `GET /batches/{id}`, `GET /photos?status=&batch_id=&cursor=`, `GET /photos/{id}/thumb` (com `ETag`), `GET /dlq` (com a causa da falha). Erros em `application/problem+json`.
- **SSE** em `GET /events` (ADR 0016) lendo o **log de eventos**: `id` = `seq`, retomada por `Last-Event-ID`, *heartbeat* (comentário a cada ~15 s, contra timeout de proxy), limite de buffer por cliente (cliente lento é desconectado e retoma pelo `seq`) e encerramento limpo no shutdown (N3).
- `POST /dlq/{id}/retry`: marca a foto como `pending`, grava nova linha no outbox **e o evento**. A semântica com a idempotência e com o lote já completo está na ADR 0018 (o `batch.completed` **não** é reemitido; a reabertura aparece no status da foto).
- CORS com allowlist de origens, token estático de demonstração (variável de ambiente) e **autenticação do SSE e das miniaturas conforme a ADR 0018**: o `EventSource` nativo não envia `Authorization`, então a hipótese é um stream via `fetch` com o cabeçalho, e miniaturas com URL não adivinhável (UUID) ou cookie de demonstração, pois `<img>` também não envia cabeçalho.
- Contrato **OpenAPI** (REST) e `specs/events.schema.json` (eventos) como fonte para o cliente e os tipos do painel (**G5**).

**Aceite**: OpenAPI e esquema de eventos validados no CI; com o worker rodando, `curl -N /events` mostra a transição `pending → done|failed` de cada foto e *heartbeats* em silêncio; reconectar com `Last-Event-ID` (inclusive depois de reiniciar o `ms-api`) não perde nem repete evento; reprocessar uma foto da DLQ a leva a `done` (ou de volta à DLQ) sem duplicar resultado; paginação por cursor estável com inserções concorrentes; origem fora da allowlist e requisição sem token são recusadas (SSE incluído); miniatura devolve `304` com `ETag`.

### Fase 7: Observabilidade, segurança e carga

- Métricas básicas (profundidade das filas, jobs por status, latência por job) via `expvar` ou Prometheus (ADR).
- **Teste de entrada hostil**: URL fora da allowlist (no ingest e no worker), **redirect para host interno**, host que resolve para IP privado, resposta lenta, corpo maior que o limite, **PNG pequeno com dimensões enormes**, payload malformado. Cada caso verifica **a classificação** (400, falha definitiva ou transitória), não só "não caiu".
- Carga: centenas de jobs; registrar throughput, memória e tamanho da imagem (**G2**, só números reais).

**Aceite**: nenhum job malicioso derruba o worker, e cada um cai na classificação esperada; números registrados no README.

### Fase 8: Painel Angular (opcional)

Só começa com a Fase 7 mergeada. Fica em **repositório separado** (`projects-ia/microservices-frontend-angular`; decisão 5 da seção 10); o contrato (OpenAPI e `events.schema.json`) é publicado por este repositório e consumido lá (ADR 0019).

- **Angular** em versão estável vigente (ADR 0019), standalone components e signals; `HttpClient` com interceptor do token; interceptor de erro que mapeia o `code` do `problem+json`; cliente e tipos TypeScript **gerados** do OpenAPI e de `events.schema.json` (G5).
- **SSE via RxJS**: um serviço que embrulha o stream (ADR 0018: `fetch` com cabeçalho) num `Observable`, com reconexão com backoff, `Last-Event-ID`, indicação de "desconectado" e *unsubscribe* ao destruir o componente (sem vazamento de conexão).
- Telas: **criar lote** (dataset normal e "com falhas"), **lote em andamento** (progresso ao vivo), **galeria de miniaturas** (paginação por cursor, `loading="lazy"`), **fila de falhas (DLQ)** com a causa e botão de reprocessar; estados de carregamento, vazio e erro em todas.
- **Execução**: serviço `web` no compose deste repositório, apontando para a imagem ou o build do repositório do painel (nginx servindo o build estático; `ng serve` só em desenvolvimento), origem registrada na allowlist de CORS; **job de CI no repositório do painel** (`npm ci`, lint, testes, build de produção, geração do cliente a partir do contrato publicado, sem diff).
- Testes: unidade dos serviços (RxJS com *marble tests*), componente, e um teste ponta a ponta (Playwright contra o compose) do fluxo "criar lote com falhas → ver na DLQ → reprocessar".

**Aceite**: com o backend no ar, um lote criado **pelo painel** aparece progredindo na tela; a foto inválida surge na DLQ com a causa; o reprocessamento atualiza a tela sem recarregar; derrubar o `ms-api` mostra "desconectado" e, ao voltar, reconecta e **recupera os eventos perdidos** pelo `Last-Event-ID`; sem vazamento de conexões SSE ao navegar entre telas; build de produção sem warning (`echo $?` = `0`); teste ponta a ponta verde no CI.

### Fase 9: Fechamento

- Checklist de long tail, README honesto (inclui: **domínio diferente do v1, comparação é de comportamento, não de dados**; o que é opcional), comparação PHP v1 × Go (imagem, memória, linhas, tempo de subida), limitações e `CHANGELOG.md`.

## 8. Estratégia de testes

| Camada | O que testa | Ferramenta |
|---|---|---|
| Unidade | regra do worker (idempotência, classificação transitório/definitivo, degrau de retry, contador de lote) com fake do broker | `testing`, tabelas |
| Integração | publicar, consumir, retry, DLQ, reconexão, outbox com duas instâncias do relay, fim de lote concorrente, contra RabbitMQ 4 e Postgres reais | `testcontainers-go` |
| Falha injetada | matar o worker em cada ponto da ordem de operações da Fase 3 | teste de integração com *kill* controlado |
| Aceite | `.http` contra o ambiente local | cliente HTTP do editor |
| Oráculo | os mesmos roteiros de mensageria no v1 PHP e no Go (parâmetros declarados) | `oraculo-mensageria.md` |
| Contrato | OpenAPI do `ms-api` × implementação × cliente gerado | validador no CI + `git diff --exit-code` |
| Concorrência | `-race` em todo `go test` | toolchain |
| Painel | unidade, componente e ponta a ponta | Jest/Vitest ou o runner da versão do Angular (ADR) |

## 9. Riscos e trade-offs

- **Domínio diferente do v1**: a paridade é de comportamento, não de dados. Assumido, vai no README.
- **Lorem Picsum não foi verificado**: a recomendação veio de memória. O S3 confirma antes da ADR 0010; se falhar, o fallback é Pexels (chave grátis, com rate limit).
- **Sem jitter nos degraus de retry**: assumido; o herd foi medido no S2 (105 de 200 reentregas em 100 ms) e fica como custo assumido; a ADR 0005 prevê a troca de mecanismo.
- **Outbox aumenta a superfície**: um relay, uma tabela e *at-least-once* na publicação. Em troca elimina a inconsistência banco × fila. Se a ADR 0012 for rejeitada, a Fase 2 precisa de outra resposta para o `503` parcial.
- **Escopo**: reconexão, idempotência, outbox e shutdown gracioso são onde a reescrita ganha valor, e onde pode crescer. Fases 3 e 4 são `L` de propósito.
- **Angular compete com a vaga-alvo React** (`published/reactjs/CLAUDE.md`, "Motivação real"): por isso o painel é **opcional** e só vem depois do backend validado.
- **Autenticação do SSE e das miniaturas**: `EventSource` e `<img>` não enviam `Authorization`. A ADR 0018 decide (stream por `fetch`, URL não adivinhável ou cookie); token estático de demonstração **não é** segurança real e o README diz isso.
- **Log de eventos cresce**: precisa de retenção; cliente que volta depois da janela recebe "recarregue o estado" em vez de eventos faltando.
- **Reprocessar a DLQ pelo painel** mexe em idempotência e no estado do lote já completo; a ADR 0018 decide antes da Fase 6.
- **Sem deploy real**: o v1 só roda local. Manter assim; deploy fica fora de escopo (regra de custo do workspace).

## 10. Decisões

Decididas em 2026-11-17 (itens 2, 4, 5 e 6) e **pendentes** (1 e 3):

1. **Onde mora o projeto e como se chama?** *Decidido de fato*: repositório privado `MarceloVilela/microservices-go` (pasta `projects-ia/microservices-go`). O v1 fica **intocado**, como oráculo.
2. **O `ms-notification` entra?** **Sim, como serviço próprio** (Fase 5).
3. **Banco**: Postgres + `sqlc` *(hipótese; ADR 0007)*. SQLite deixaria de servir ao relay do outbox (`SKIP LOCKED`).
4. **Loggly**: **descartado** (o v1 nunca confirmou eventos lá). Em troca, a notificação ganha a abstração `Notifier` (ADR 0020) com `slog` como padrão e e-mail via SMTP (Mailpit no desenvolvimento) como destino opcional.
5. **Frontend**: **repositório separado** (`projects-ia/microservices-frontend-angular`, portfólio Angular isolado). Consequência: o **G5** passa a cruzar dois repositórios (o contrato é publicado aqui e o cliente é gerado lá; ADR 0019).
6. **`ms-api` separado do `ms-ingest`**: **sim**.

## Ordem de execução resumida

`Fase 0` (spec, ADR, spikes, **G0**) → `Fase 1` (esqueleto) → `Fase 2` (ingest e outbox) → `Fase 3` (worker e fim de lote) →
`Fase 4` (resiliência, **G1**) → `Fase 5` (notification) → `Fase 6` (`ms-api`, **G5**) → `Fase 7` (observabilidade, segurança e carga) →
`Fase 8` (painel Angular, opcional) → `Fase 9` (fechamento). Um PR por fase, só a pedido seu.

## Fontes

- `projects-ia/ec-microservices-rabbitmq/specs.md` e `CLAUDE.local.md` (requisitos R1 a R8, status e code review do v1).
- `published/php-laravel/plan.md` e `specs/adr/` (método, gates e formato de ADR).
- `go101.md` (decisões de escopo desta conversa, antigo `zgo.md`) e `projects-ia/microservices-frontend-angular/docs/angular101.md` (painel Angular e `ms-api`, antigo `zangular.md`; o plano do painel é `plan-angular.md` na mesma pasta).
- `plan-go-code-review.md` (revisão `high` deste plano e rastreio das correções).
