# platform

Módulo Go compartilhado por todos os serviços (ADR 0015). É o ponto de maior acoplamento do repositório: mudar aqui afeta `ms-ingest`, `ms-worker`, `ms-notification`, `ms-api` e `ms-queues`.

## Regra de dependência (gate G4)

Imposta por `archtest/` e rodada no `make check`:

- serviços importam **só** `platform`, nunca uns aos outros;
- `platform` **não** importa nenhum serviço;
- só `ms-queues` e `platform/amqpx` podem declarar a topologia do broker.

Não ponha aqui regra de negócio de um serviço só: o que serve a um único serviço fica em `internal/` dele.

## Pacotes

| Pacote | Papel |
|---|---|
| `service` | bootstrap comum: subcomandos `serve`, `healthcheck` e `config check`, logger, servidor HTTP e shutdown gracioso |
| `config` | lê e valida variáveis de ambiente no boot; segredos não vão para o log |
| `logging` | `*slog.Logger` JSON |
| `health` | `/healthz` (liveness) e `/readyz` (readiness) |
| `httpx` | erros `application/problem+json`, CORS por allowlist, token de demonstração, corpo com limite; só stdlib (ADR 0008) |
| `metrics` | `/metrics` Prometheus: registro por processo, middleware HTTP com a rota como rótulo, cache para consultas caras por scrape (ADR 0009) |
| `amqpx` | isola o `amqp091-go`: conexão, declaração, publisher com confirms e `Supervisor` com reconexão (ADR 0003) |
| `topology` | nomes, chaves e argumentos do exchange e das filas (`photos`, `photo.process`, escada de retry, DLQ, `batch.completed`) |
| `messages` | contrato JSON das mensagens do broker (`photo.process`, `batch.completed`), ids e cabeçalhos |
| `db` | pool `pgx`, verificação do esquema e migrações embutidas (`db/migrations`, `0001` a `0004`) |
| `outbox` | relay do outbox transacional (`FOR UPDATE SKIP LOCKED`, confirms, quarentena); ADR 0012 e 0021 |

## Cuidado ao mudar

1. **Mensagens e `topology`**: são contrato entre serviços e com a fila já declarada. Argumento diferente de uma fila existente dá `PRECONDITION_FAILED` (declare de novo só com o broker limpo).
2. **Migrações**: só se acrescenta (`0005_...`); só o `ms-ingest` aplica, os outros verificam.
3. Rode `make check` e `make integration` antes de qualquer PR que toque aqui; o `/code-review` deste diff é `high` (regra do projeto).

## Testes

`go test -race ./...` dentro do módulo (ou `make check` na raiz). Os testes com Postgres e RabbitMQ reais ficam em `tests/` (`make integration`).
