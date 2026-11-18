# Fase 1: esqueleto (monorepo Go, Docker, CI, lint e topologia do broker)

Branch: `fase-1-esqueleto` (a partir da `main` com a Fase 0 mergeada). Plano: `docs/plan-go.md` (seção 7, Fase 1).
ADRs exercitadas: 0002 (módulos), 0011 (imagem), 0015 (`platform` e `topology-init`), 0003 (cliente AMQP).

## Decisões da fase

| # | Decisão | Motivo |
|---|---|---|
| F1-1 | Go **1.27.4** (estável vigente em 2026-11-18) fixado em `go.work`, nos `go.mod` e no `Dockerfile` (`golang:1.27.4-alpine`) | ADR 0011 manda fixar na Fase 1 |
| F1-2 | Módulos: `platform`, `ms-ingest`, `ms-worker`, `ms-notification`, `ms-api`, `ms-queues` (só `topology-init`), `archtest` (G4) e `tests` (integração com `testcontainers-go`), unidos por `go.work` | ADR 0002; `testcontainers` fica em módulo à parte para não contaminar o grafo de `platform` |
| F1-3 | Bootstrap comum em `platform/service`: subcomandos `serve` (padrão), `healthcheck` e `config check`; cada `main` só declara nome, porta padrão e a topologia que exige | evita copiar o mesmo `main` em quatro serviços |
| F1-4 | **Definição da topologia** (nomes, TTLs, chaves) em `platform/topology`; **declaração** só em `ms-queues/cmd/topology-init`; os serviços só **verificam** (passivo) | ADR 0015, dono único |
| F1-5 | "Topologia divergente" = exchange ou fila **ausente** (`*DeclarePassive`). Diferença de **argumentos** não é detectável por declaração passiva; ela falha no `topology-init` (`PRECONDITION_FAILED`, que é alto e no boot) | limite assumido, registrado aqui |
| F1-6 | Sem Postgres na Fase 1 (nenhum serviço usa banco ainda); entra na Fase 2 | evitar infraestrutura sem consumidor |
| F1-7 | `internal/{domain,app,adapters}` **não** é criado vazio: nasce com o primeiro código (Fase 2). Na Fase 1 cada serviço só tem `cmd/` | D6 "revisitar na Fase 1": diretório vazio é cerimônia sem valor |
| F1-8 | Teste de HTTPS na imagem: estágio `https-probe` do `Dockerfile` na **mesma base final** (`distroless/static:nonroot`) | prova o bundle de CA da base; o download real com o binário do worker é testado na Fase 3 (**limite**) |
| F1-9 | Segredo: `run.sh` gera `.env` (ignorado pelo git) com senha aleatória do RabbitMQ; `.env.example` só com nomes | plano: sem segredo versionado |

## Topologia (definida em `platform/topology`)

Exchange `photos` (direct, durável). Filas **clássicas e duráveis** (ADR 0005):

| Fila | Chave de roteamento | Argumentos |
|---|---|---|
| `photo.process` | `process` | nenhum |
| `photo.process.retry.5s` / `.30s` / `.2m` / `.5m` | `retry.5s` / `retry.30s` / `retry.2m` / `retry.5m` | `x-message-ttl` (5000, 30000, 120000, 300000), `x-dead-letter-exchange=photos`, `x-dead-letter-routing-key=process` |
| `photo.process.dlq` | `dlq` | nenhum |
| `batch.completed` | `batch.completed` | nenhum |

Verificação por serviço: `ms-ingest` = exchange + `photo.process` + `batch.completed`; `ms-worker` = exchange + `photo.process`,
os quatro degraus e a DLQ; `ms-notification` = `batch.completed`; `ms-api` = `photo.process.dlq`.

## Variáveis de ambiente (lista completa na Fase 1)

`LOG_LEVEL` (`debug|info|warn|error`, padrão `info`), `HTTP_ADDR` (padrão por serviço),
`AMQP_URL` (obrigatória nos serviços; no `topology-init` também), `READY_TIMEOUT` (padrão `2s`), `READY_CACHE_TTL` (padrão `2s`: o `/readyz` reaproveita o último resultado). `HTTP_ADDR` é validada (`host:porta`, porta 0 a 65535). Nomes só `[A-Z0-9_]`.
`config check` valida **todas**, lista os **nomes** (nunca valores) e sai com `0` ou `1`; o `serve` roda a mesma validação no boot.

## Entregáveis

1. `go.work`, módulos e `Makefile` (`build`, `vet`, `test`, `lint`, `vuln`, `arch`, `integration`, `check`, `images`).
2. `platform/config`, `platform/logging`, `platform/topology`, `platform/amqpx` (dial, declare, verify), `platform/health`, `platform/service`.
3. `ms-queues/cmd/topology-init`; `cmd/` dos quatro serviços com `/healthz`, `/readyz` e `healthcheck`.
4. `Dockerfile` multi-stage único (`ARG PKG`), `docker-compose.yml` por serviço, rede externa `ms-go-network`, `run.sh`, `stop.sh`, `.env.example`.
5. `archtest` (G4), `.golangci.yml`, `.github/workflows/ci.yml`.
6. `scripts/aceite-fase-1.sh` (aceite ponta a ponta) e `specs/execucao-fase-1.log` com a saída.

## Testes (escritos antes do código)

- `platform/config`: nome de variável inválido, valor inválido, `config check` lista nomes sem valores.
- `platform/topology`: tabela consistente (chaves únicas, TTL crescente, DLX dos degraus).
- `platform/health`: `/healthz` sempre 200; `/readyz` 200 com checagem ok e 503 com erro, com timeout.
- `platform/service`: `healthcheck` sai 0 contra servidor de pé e 1 sem servidor; subcomando desconhecido sai 2.
- `archtest`: serviço não importa outro serviço; `platform` não importa serviço; `internal/domain` não importa `adapters`.
- `tests` (integração, `testcontainers-go`, RabbitMQ 4): `topology-init` cria tudo e é idempotente; `Verify` passa depois dele e **falha** com fila ausente.

## Aceite (do plano) e como se prova

| Critério | Prova |
|---|---|
| `docker compose up` sobe tudo | `./run.sh` e `scripts/aceite-fase-1.sh` |
| painel `:15672` mostra a topologia criada **só** pelo `topology-init` | API de gerenciamento lista exchange e 7 filas; os serviços não têm `Declare` não passivo (conferido por `grep`/arch) |
| serviço com topologia divergente falha o `/readyz` em vez de recriar | apaga uma fila, `/readyz` dá 503 e a fila **continua** ausente |
| download HTTPS funciona na imagem final | `docker build --target https-probe` + `docker run` |
| CI verde reproduzido localmente (`echo $?` = `0`) | `make check` sem pipe, e em container limpo |

## Estado (2026-11-18)

**Implementada; aceite verde.** Evidência em `specs/execucao-fase-1.log`: `make check` = 0 (build, vet, `test -race`, `archtest`,
`golangci-lint` 2.15.0, `govulncheck`), `make integration` = 0 (5 testes contra RabbitMQ 4 real), `scripts/aceite-fase-1.sh` = 0
(5 blocos), e `make check` reproduzido em `golang:1.27.4-bookworm` limpo = 0. Imagens: 18,4 MB por serviço, 16,2 MB o `topology-init`.
PR **só quando o usuário pedir** (antes: `/code-review high`, por mexer em `platform/` e topologia).

### Limites e achados (não escondidos)

- **F1-5:** `Verify` só vê ausência. Argumento divergente (ex.: TTL de degrau) só aparece no `topology-init` (`PRECONDITION_FAILED`,
  provado em `TestDeclareFalhaComArgumentosDivergentes`); um serviço já de pé não percebe. Se importar, a API de gerenciamento resolve (fica para a Fase 4/7).
- **F1-8:** a prova de HTTPS usa a base final, não o binário do worker; o download real fica para a Fase 3.
- `/readyz` ainda abre uma conexão AMQP por **verificação** (agora no máximo uma a cada `READY_CACHE_TTL`); a conexão persistente com reconexão entra na Fase 4 (ADR 0003).
- `ms-notification` e `ms-api` ainda não consomem nada; só verificam a fila que vão usar.
- O CI (`.github/workflows/ci.yml`) **nunca rodou no GitHub**: foi reproduzido só localmente. O 1º run real é o do PR.
- O aceite e a integração precisam do Docker do host; o container limpo cobre só `make check`.
- `go.work.sum` não foi gerado (nenhuma dependência o exigiu). `govulncheck` fixado em v1.10.0 (a base de vulnerabilidades ainda é a do dia, então o CI pode ficar vermelho por CVE nova, o que é desejado).
- Disco da máquina em 94% (12 GB livres) depois da fase.

### Code review `high` (2026-11-18) e correções

10 achados, todos tratados e **recompilados e reexecutados** (ver `specs/execucao-fase-1.log`, seção "Rodada 2"):

| Achado | Correção |
|---|---|
| `Dial` customizado sem prazo de handshake | prazo do contexto (ou 30 s) aplicado à conexão; `Verify`/`Declare` fecham a conexão quando o contexto termina; testes contra um servidor TCP mudo |
| `topology-init` tratava erro transitório de `Declare` como permanente | `amqpx.IsPermanent` (só `PRECONDITION_FAILED` e `ACCESS_REFUSED`); 10 s por tentativa dentro dos 60 s |
| `HTTP_ADDR` sem validação; healthcheck só em 127.0.0.1 | validador `host:porta`; healthcheck usa o host configurado (loopback se `:porta` ou `0.0.0.0`); teste com `[::1]` |
| `/readyz` abre uma conexão por chamada | cache de `READY_CACHE_TTL`, com chamadas concorrentes esperando a mesma verificação |
| binários ELF (~48 MB) na árvore | removidos; `.gitignore`/`.dockerignore`; `make build` usa `-o /dev/null` |
| `archtest` rodando duas vezes; `tests` fora do build | `TEST_MODULES` sem `archtest`; `tests` no loop de build |
| `govulncheck@latest` | fixado em v1.10.0 |
| teste sem esperar o `Run` e sem timeout | espera o encerramento e usa cliente com timeout |
| docs: "12 restantes", D1 com ADR errada, D6 "revisitar" | corrigidos em `docs/adr/README.md` e `docs/plan-go.md` |
