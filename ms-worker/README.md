# ms-worker

Consome `photo.process`, baixa a imagem com defesas de SSRF, gera a miniatura e fecha o lote.

## O que faz (ordem por mensagem)

1. Checa o estado da foto no banco (idempotência por `message_id`, ADR 0006).
2. Baixa a imagem e gera a miniatura em arquivo temporário (`image.DecodeConfig` antes de decodificar).
3. `fsync` + `rename` atômico para `THUMBS_DIR`.
4. **Uma transação**: estado da foto, contador do lote, evento (`events`) e, se for a última foto, o outbox de `batch.completed`.
5. Só então dá **ack**.

Falha **transitória** (timeout, 5xx, 429, infra) segue a escada `photo.process.retry.{5s,30s,2m,5m}`; falha **definitiva** (404, imagem inválida, host fora da allowlist, pixels acima do teto) vai direto a `photo.process.dlq` e **conta como foto terminada** (ADR 0005, `docs/dlq.md`). Reconecta ao broker sozinho (`amqpx.Supervisor`) e, no shutdown, drena o que está em voo.

## Download seguro (N4)

Allowlist de hosts, `CheckRedirect`, checagem do IP resolvido, limite de bytes e limite de pixels. Detalhes em ADR 0010.

## Ordem (R3)

FIFO só vale com `WORKER_CONCURRENCY=1` e `WORKER_PREFETCH=1`, o padrão do compose (ADR 0013). Suba os dois para ganhar vazão e perder a ordem.

## Endpoints (porta 8083)

`GET /healthz`, `GET /readyz`, `GET /metrics`. Não há API de negócio.

## Configuração

| Variável | Padrão |
|---|---|
| `AMQP_URL`, `DATABASE_URL` | obrigatórias |
| `HTTP_ADDR` | `:8083` |
| `WORKER_CONCURRENCY` / `WORKER_PREFETCH` | `1` / `1` |
| `THUMBS_DIR` | `/data/thumbs` (volume `ms-go-thumbs`, lido pelo `ms-api`) |
| `ALLOWED_HOSTS` | `picsum.photos,fastly.picsum.photos` |
| `FETCH_TIMEOUT` | `10s` |
| `MAX_IMAGE_BYTES` / `MAX_PIXELS` | `10485760` / `25000000` |
| `SHUTDOWN_TIMEOUT` / `CONSUMER_BACKOFF_MAX` | `10s` / `30s` |

`stop_grace_period` do compose é 30 s: maior que `SHUTDOWN_TIMEOUT` + `FETCH_TIMEOUT`.

## Estrutura

`internal/domain` (classificação de falha), `internal/app` (processador e passos), `internal/adapters/{consumer,httpfetch,thumb,filestore,postgres,promrec}`, `internal/fault` (a build tag `faultinject` injeta *kill* em testes e **nunca** vai à imagem).

## Rodar e testar

`./run.sh` (raiz), `make check`, `make integration`. Spec: `specs/fase-3-worker.md` e `specs/fase-4-resiliencia.md`.
