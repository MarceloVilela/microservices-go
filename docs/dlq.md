# Inspecionar a DLQ (`photo.process.dlq`)

A DLQ guarda o que não vai mais ser tentado: falha **definitiva** (404, imagem inválida, host fora da allowlist, pixels acima do teto,
mensagem inválida), **transitória esgotada** (os 4 degraus de retry acabaram) e **infraestrutura esgotada** (banco ou disco do worker
fora por todos os degraus; a foto continua `pending`). Reprocessar é da Fase 6 (`POST /dlq/{id}/retry`); aqui só se **olha**.

Cabeçalhos da mensagem na DLQ:

| Cabeçalho | Valor |
|---|---|
| `x-failure-class` | `permanent`, `exhausted` ou `infrastructure` |
| `x-failure-reason` | motivo curto (`HTTP 404`, `tentativas esgotadas: HTTP 503`...); nunca traz URL nem dado do usuário |
| `x-attempt` | degraus de retry percorridos |

## Pelo script (sem consumir)

```sh
scripts/dlq.sh        # até 20 mensagens, devolvidas à fila
scripts/dlq.sh 100
```

## Pelo painel do RabbitMQ

`http://localhost:15672` → Queues → `photo.process.dlq` → **Get messages**, com `Ack Mode = Nack message requeue true`
(**nunca** `Automatic ack` nem `Reject requeue false`: removeriam a mensagem).

## Pela API de gerenciamento

```sh
curl -u "$RABBITMQ_USER:$RABBITMQ_PASS" -H 'Content-Type: application/json' \
  -d '{"count":10,"ackmode":"ack_requeue_true","encoding":"auto"}' \
  http://localhost:15672/api/queues/%2F/photo.process.dlq/get
```

## Cruzando com o banco

```sh
docker exec ms-db psql -U app -d app -c "SELECT id, status, failure_class, failure_reason FROM photos WHERE status='failed'"
```

Mensagem na DLQ com a foto `done` é a janela residual da D2 (entrega concorrente da mesma foto); é inofensiva e o retry da Fase 6 a trata como no-op.
