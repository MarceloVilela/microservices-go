# ms-queues

O RabbitMQ 4 do projeto (`rabbitmq:4-management-alpine`) e o comando `topology-init`, **dono único da topologia** (ADR 0015).

## Portas

| Porta | Uso |
|---|---|
| `127.0.0.1:5672` | AMQP |
| `127.0.0.1:15672` | painel de gerenciamento (usuário e senha em `RABBITMQ_USER` / `RABBITMQ_PASS` do `.env`) |

## Topologia

Exchange `photos`. Filas: `photo.process`, a escada de retry `photo.process.retry.{5s,30s,2m,5m}` (TTL fixo e DLX de volta; ADR 0005), `photo.process.dlq` e `batch.completed`. A definição está em `platform/topology`.

Só o `topology-init` declara. Os outros serviços verificam com `QueueDeclarePassive` e falham o `/readyz` se algo divergir. Argumentos diferentes de uma fila existente dão `PRECONDITION_FAILED`, tratado como falha permanente.

## `topology-init`

Execução única, chamada pelo `run.sh` (`docker compose run --rm topology-init`, perfil `init`). Espera o broker por até 60 s (tentativas de 10 s, 2 s entre elas) e sai com `0` ou `1`. Imagem `final-job` (16 MB). Variáveis: `AMQP_URL` (obrigatória) e `LOG_LEVEL`.

## Atenção

Na **1ª partida** de um container novo o RabbitMQ às vezes cai com `eacces` no `.erlang.cookie` (causa desconhecida; `specs/spikes/s1-amqp.md`). O compose usa `restart: on-failure:3` e o `topology-init` tenta de novo; se persistir, `docker restart ms-queues`.

## Rodar

`./run.sh` (raiz) sobe o broker, roda o `topology-init` e depois os serviços. Spec: `specs/fase-1-esqueleto.md`.
