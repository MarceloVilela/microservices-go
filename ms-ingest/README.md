# ms-ingest

Porta de entrada do pipeline: recebe um lote de fotos, grava tudo no Postgres e publica as mensagens de processamento por **outbox**.

## O que faz

- `POST /batches`: valida o corpo, grava lote, fotos e linhas do **outbox na mesma transação** e responde `202`. `Idempotency-Key` repetida não cria segundo lote (ADR 0012).
- **Relay**: lê o outbox com `FOR UPDATE SKIP LOCKED` e publica `photo.process` com *publisher confirm*. Linha que falha demais vai para **quarentena** (ADR 0021). Pode haver mais de uma instância.
- Valida a URL de cada foto contra a allowlist (`ALLOWED_HOSTS`) já na entrada; o worker valida de novo no download.
- É o **único** serviço que aplica as migrações (`platform/db/migrations`); worker, notification e api só verificam o esquema.
- Com o broker fora, o `POST` ainda responde `202` (as linhas esperam no outbox) e `/readyz` falha.

## Endpoints (porta 8081)

| Rota | Descrição |
|---|---|
| `POST /batches` | cria o lote (contrato: `specs/openapi-ingest.yaml`) |
| `GET /healthz`, `GET /readyz` | liveness e readiness (Postgres e broker) |
| `GET /metrics` | Prometheus |

## Configuração

| Variável | Padrão | Nota |
|---|---|---|
| `AMQP_URL`, `DATABASE_URL` | (obrigatórias) | o compose monta a partir do `.env` gerado pelo `run.sh` |
| `HTTP_ADDR` | `:8081` | |
| `ALLOWED_HOSTS` | `picsum.photos,fastly.picsum.photos` | |
| `CORS_ORIGINS` | vazio | origens do painel |
| `DEMO_TOKEN` | vazio | token de demonstração (**não é segurança real**) |
| `RELAY_POLL_INTERVAL` / `RELAY_BATCH_SIZE` / `RELAY_MAX_ATTEMPTS` | `500ms` / `100` / `10` | |

## Estrutura

`cmd/ms-ingest` (composição e `healthcheck`), `internal/domain`, `internal/app`, `internal/adapters/{httpapi,postgres}`. O relay e o outbox ficam em `platform/outbox`.

## Rodar e testar

```sh
./run.sh                      # na raiz: sobe o stack todo
make check                    # build, vet, test -race, arch, lint, vuln
make integration              # testes com Postgres e RabbitMQ reais (Docker)
```

Roteiro manual: `specs/acceptance/`. Spec: `specs/fase-2-ingest-outbox.md`.
