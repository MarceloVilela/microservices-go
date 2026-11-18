# S1: `amqp091-go`, publisher confirms e reconexão (RabbitMQ 4)

Executado em 2026-11-17. Código em `specs/spikes/s1-amqp/` (`main.go`, módulo isolado); saída bruta em `specs/spikes/s1-resultado.txt`.

**Ambiente:** Go 1.27.4 (linux/amd64), `amqp091-go` v1.17.0, RabbitMQ **4.3.6** (`rabbitmq:4-management-alpine`, container local, broker em `127.0.0.1`), fila **clássica durável** `s1.photo.process`, mensagens `DeliveryMode: Persistent`. Uma única execução, na máquina de desenvolvimento, sem carga concorrente.

## Resultados

| Teste | Resultado |
|---|---|
| 1.000 publicações, **um confirm por mensagem** (publica e espera) | total 507 ms; ~1.973/s; **p50 430 µs, p95 796 µs, p99 1,7 ms, máx 8,0 ms** |
| 1.000 publicações, **confirm em lote** (publica tudo, espera no fim) | total 32 ms; ~31.196/s |
| Reinício do broker (`docker restart`) no meio de 200 publicações com reenvio | 200 confirmadas, **1 reenvio**, 7 tentativas de conexão com backoff (200 ms a 3 s), 29,4 s no total |
| Fila depois do reinício | `messages_ready` = **2.200** (2.000 do teste de latência + 200 do de reconexão): **nenhuma perdida e nenhuma duplicada** nesta execução |

## Conclusões

- **Confirms funcionam** com RabbitMQ 4.3 e `amqp091-go`: `PublishWithDeferredConfirmWithContext` + `WaitContext`. O custo por confirm é sub-milissegundo localmente (p99 1,7 ms), então o critério de reversão da ADR 0004 ("latência inaceitável") **não foi acionado**.
- **Confirm em lote é ~16x mais rápido** que um por mensagem neste teste, o que favorece o relay do outbox publicar em lote (ADR 0012).
- **Reconexão exige código nosso**, como previsto (ADR 0003): a conexão cai, o publish falha com `504 channel/connection is not open`, e o laço de reconexão com backoff reabre conexão, canal, declara a fila e liga os confirms. A mensagem sem confirm foi **reenviada**; isso é *at-least-once* (um reenvio entre o confirm e a perda da resposta poderia duplicar; o worker é idempotente, ADR 0006).
- **Persistência:** fila durável + mensagem persistente sobreviveram ao reinício do broker (O4 do oráculo, lado Go).

## Limites desta evidência

- Uma execução, localhost, sem rede real nem broker sob carga; **não** repetido para variância.
- Fila **clássica**; quorum queues (ADR 0005, S2) não foram testadas aqui. Quorum queues costumam ter latência de confirm maior (consenso Raft), então o número **não** vale para elas.
- Não testei queda do canal sem queda da conexão, nem `NotifyReturn`/`mandatory`.
- Não testei o **consumidor** (ack manual, reconexão do consumer); isso é Fase 3 e 4.
- O reinício foi do container inteiro (conexão derrubada), não de um corte de rede.

## Adendo: quorum queue (medido depois, mesma máquina e broker 4.3.6)

Para a comparação da ADR 0005 repeti o teste com `QUEUE_TYPE=quorum` (saída bruta em `specs/spikes/s1-quorum-resultado.txt`):

| Teste | Clássica (controle, mesma execução) | Quorum |
|---|---|---|
| 1.000 publicações, um confirm por mensagem | p50 423 µs, p95 725 µs, p99 1,3 ms; ~2.091/s | **p50 5,3 ms, p95 8,5 ms, p99 11,3 ms; ~173/s** |
| Confirm em lote | ~30.708/s | ~10.192/s |
| Reinício do broker no meio de 200 publicações | 29,4 s, 1 reenvio (execução do S1) | **67,1 s**, 1 reenvio, 7 tentativas de conexão; 200 mensagens na fila, nenhuma perdida e nenhuma duplicada |

Uma execução de cada; um nó só. A quorum é ~12× mais lenta por confirm e mais lenta para voltar após reinício, sem perder dados. (Um primeiro teste de reconexão em quorum foi descartado: o broker não estava de pé quando ele começou.)

## Achado lateral (**corrigido depois**; não reproduzível)

Nas 3 primeiras tentativas desta sessão (2026-11-17, ~19:16 a 19:18 UTC), `docker run` de `rabbitmq:4-management-alpine` (imagem local de 2026-09-29) falhou como root com `Error when reading /var/lib/rabbitmq/.erlang.cookie: eacces`, inclusive com `RABBITMQ_ERLANG_COOKIE`. Subiu com `--user rabbitmq`, e foi assim que o S1 rodou. **Mais tarde (~19:45 UTC) o mesmo container subiu como root, sem `--user`, tanto pelo `docker-compose` do v1 quanto por `docker run` em bridge e na `ec-network`**; não consegui reproduzir o erro e **não sei a causa**. A suposição anterior de que o compose do v1 teria o mesmo problema **estava errada**: o v1 subiu sem ajuste. **Nova ocorrência (2026-11-17, ~19:58 UTC):** o `docker run` sem `--user` falhou de novo na **primeira** partida (mesmo `eacces`) e, depois de um `docker restart`, o mesmo container subiu normalmente (o cookie ficou `-r-------- rabbitmq`). Parece intermitente na primeira partida de um container novo; a causa continua desconhecida. Não tratar `user: rabbitmq` como requisito da Fase 1 sem reproduzir o erro, mas prever **uma nova tentativa** na subida do broker em scripts e testes.
