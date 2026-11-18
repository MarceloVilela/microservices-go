# Oráculo de mensageria (v1 × Go)

> Entregável 2 da Fase 0. Cenários **observáveis** do v1 que o Go precisa reproduzir (G1). Cada um é um roteiro
> executável contra o v1 **e** contra o Go, com os parâmetros do Go declarados. Divergência só entra por ADR
> **aprovada** (nunca reescrevendo o cenário em silêncio).
>
> **Estado:** lado **v1 executado** em 2026-11-17 (commit `9302ac7`, RabbitMQ 4.3.6; saída bruta em
> `specs/oraculo-v1-resultado.txt`) e o resultado bate com a leitura do código. Lado **Go: não executado**
> (não há código; roda a partir das Fases 3 e 4).

Comandos auxiliares (v1 e Go, broker em container `ms-queues`):

```bash
docker exec ms-queues rabbitmqctl list_queues name messages_ready messages_unacknowledged   # contagem por fila
```

## O1 (R2): publicar sem esperar resposta

| | v1 | Go |
|---|---|---|
| Passos | `POST /transaction` (`client.http`) com o `ms-notification` **parado** | `POST /batches` com o `ms-worker` **parado** |
| Esperado | resposta de sucesso imediata; 1 mensagem em *Ready* na fila `notification_queue` | `202` imediato; N mensagens em *Ready* na fila `photo.process` (N = fotos do lote) |
| Parâmetros do Go | — | broker no ar; lote do `dataset-de-paridade.md` |
| Diferença assumida | — | mais forte: mensagens persistentes, publicadas com confirm pelo relay do outbox |

## O2 (R3): consumo FIFO

| | v1 | Go |
|---|---|---|
| Passos | publicar 3 transações em sequência, subir o consumidor (`php artisan notification`) | publicar um lote de 3 fotos, subir o worker |
| Esperado | saída `Printing:` na **mesma ordem** da publicação | miniaturas e eventos na **mesma ordem** de `seq` |
| Parâmetros do Go | — | **`WORKER_CONCURRENCY=1` e `prefetch=1`** (modo oráculo; ADR 0013) |
| Diferença assumida | — | com concorrência maior a ordem **não** é garantida (ADR 0013) |

## O3 (R5a): consumidor parado, mensagens ficam em *Ready*

| | v1 | Go |
|---|---|---|
| Passos | parar o `ms-notification`; publicar 2 transações; `list_queues` | parar o `ms-worker`; publicar um lote; `list_queues` |
| Esperado | `messages_ready` = 2 | `messages_ready` = N |
| Depois | subir o consumidor: `messages_ready` volta a 0 e as 2 mensagens são entregues | subir o worker: `messages_ready` = 0 e todas as fotos chegam a `done`/`failed` |

## O4 (R5b): broker reiniciado

| | v1 | Go |
|---|---|---|
| Passos | publicar 2 transações com o consumidor parado; `docker restart ms-queues`; `list_queues` | idem, com um lote |
| Esperado | **mensagens perdidas** (não persistentes, `auditoria-v1.md` A4): `messages_ready` = 0 | **mensagens preservadas** (fila durável + mensagem persistente): `messages_ready` = N |
| Diferença assumida | o v1 **não** cumpre isto | requisito mais forte que o v1 (RQ5); registrar o resultado do v1 como baseline, não como alvo |

## O5: consumidor derrubado no meio de uma mensagem (novo, sem equivalente no v1)

| | v1 | Go |
|---|---|---|
| Passos | n/a | matar o worker em cada ponto da Fase 3 (após o temporário, após o `rename`, após o commit e antes do ack) |
| Esperado | `no_ack=true`: a mensagem é **perdida** (A3) | mensagem **reentregue**, sem duplicar resultado nem deixar miniatura truncada |

## Critério de G1

O1, O2, O3 passam no Go com os parâmetros declarados; O4 passa no Go (mais forte que o v1); O5 passa no Go. A
diferença de O2 com concorrência > 1 está registrada na ADR 0013.

## Resultado do lado v1 (executado)

Pilha do v1 subida pelos próprios `docker-compose` (sem alterar o código do v1; seed: 10 usuários e 50 transações),
um `POST /transaction` por mensagem, consumo com `php artisan notification` (no container `notification-app`).

| Cenário | Observado no v1 | Bate com o esperado? |
|---|---|---|
| **O1** publicar sem esperar | `POST` com `http=200`; fila `notification_queue` com **1** mensagem em *Ready* | sim |
| **O2** FIFO | 3 mensagens (valores 101, 102, 103; transações 53, 54, 55) impressas **na ordem de publicação**; fila a 0 | sim |
| **O3** consumidor parado, entrega na volta | 2 em *Ready* com o consumidor parado; ao voltar, as 2 impressas em ordem e fila a 0 | sim |
| **O4** broker reiniciado | 2 em *Ready* antes do `docker restart ms-queues`; **0 depois** (a fila durável continua a existir, as mensagens se perderam) | sim: o v1 **não** cumpre; é o baseline |
| **O5** consumidor derrubado com mensagem em voo | com o consumidor congelado (SIGSTOP) o broker entregou as 3 mensagens (`Ready` 0 e `Unacked` 0, por causa do `no_ack=true`); depois do `kill -9` a fila segue em 0 e **nenhuma foi impressa**, nem por um novo consumidor | sim: as 3 foram **perdidas** |

Como O5 foi montado: o consumidor do v1 só imprime e não tem ponto de falha no meio da mensagem, então o roteiro congela o processo, publica, e mata o processo; é uma simulação da perda com `no_ack`, não uma queda no meio do processamento real.

Critério de G1: baselines do v1 registrados. O Go precisa igualar O1 a O3 e superar O4 e O5 (RQ5 e N2).
