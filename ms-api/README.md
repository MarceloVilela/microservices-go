# ms-api

Leitura do estado do pipeline, miniaturas, reprocessamento da DLQ e stream de eventos (SSE). É o que o painel consome.

## Endpoints (porta 8082)

Contrato: `specs/openapi-api.yaml`; eventos: `specs/events.schema.json`. **Tudo exige `Authorization: Bearer <DEMO_TOKEN>`** (token de demonstração, **não é segurança real**); `/healthz`, `/readyz` e `/metrics` ficam de fora.

| Rota | Descrição |
|---|---|
| `GET /batches`, `GET /batches/{id}` | lotes e progresso |
| `GET /photos` | fotos, paginação por cursor |
| `GET /photos/{id}/thumb` | miniatura, com `ETag` e `304` |
| `GET /dlq` | fotos que falharam, com a causa (inclui `pending` por infra esgotada) |
| `POST /dlq/{id}/retry` | reabre foto `failed`; `409` se não estiver `failed`, `404` se não existir |
| `GET /events` | SSE |

## SSE (ADR 0016 e 0018)

- Vem da tabela `events` (escrita na mesma transação de cada mudança de estado) e de `LISTEN/NOTIFY`; **nunca** de polling nem de mensagens do broker.
- `seq` é o `Last-Event-ID`: o cliente que reconecta recebe o que perdeu. Fora da janela de retenção (`EVENTS_RETENTION`), recebe `reset` e deve recarregar o estado.
- O cliente usa `fetch` com `Authorization` (o `EventSource` não envia cabeçalho). Heartbeat periódico; no shutdown, `event: shutdown`.
- Um buraco no `seq` espera 2 s antes de ser aceito (transação ainda não confirmada).

## Configuração

| Variável | Padrão | Nota |
|---|---|---|
| `AMQP_URL`, `DATABASE_URL` | obrigatórias | |
| `DEMO_TOKEN` | **obrigatória** | o compose lê `API_DEMO_TOKEN` do `.env`, separado do token do `ms-ingest` |
| `HTTP_ADDR` | `:8082` | |
| `CORS_ORIGINS` | vazio | o compose usa `API_CORS_ORIGINS` (`WITH_WEB=1` acrescenta `http://localhost:8080`) |
| `THUMBS_DIR` | `/data/thumbs` | volume `ms-go-thumbs`, **somente leitura** aqui |
| `SSE_HEARTBEAT` / `SSE_WRITE_TIMEOUT` | `15s` / `5s` | |
| `EVENTS_RETENTION` | `24h` | |

`stop_grace_period` de 20 s: maior que o shutdown do servidor (10 s), para os streams fecharem.

## Estrutura

`internal/app`, `internal/contract` (teste de contrato: rotas × OpenAPI), `internal/adapters/{httpapi,postgres,sse,thumbs}`.

## Rodar e testar

`./run.sh` (raiz), `make check`, `make integration`, `make accept` (aceite 6). Spec: `specs/fase-6-api.md`.
