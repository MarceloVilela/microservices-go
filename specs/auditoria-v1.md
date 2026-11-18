# Auditoria do v1 (`ec-microservices-rabbitmq`)

> Entregável 1 da Fase 0. Fonte: **leitura do código** do v1 (commit `9302ac7`, `main`), não só do `specs.md`.
> Caminhos relativos ao repositório do v1 (`projects-ia/ec-microservices-rabbitmq`).
> Arquivos lidos: `ms-transaction/src/app/{Brokers/RabbitMQ.php,Services/TransactionService.php,Http/Controllers/TransactionController.php}`,
> `ms-notification/src/app/{Console/Commands/NotificationCommand.php,Services/NotificationService.php,Http/Controllers/NotificationController.php}`,
> `ms-queues/docker-compose.yml`, `run.sh`, `stop.sh`, `client.http`, `bootstrap/app.php`, `.env.example`.
> Decisão por achado: **herdar**, **corrigir** (vira requisito do Go) ou **descartar**.

## Achados

| # | Achado (evidência) | Gravidade | Decisão | Onde vira requisito no Go |
|---|---|---|---|---|
| A1 | `RabbitMQ.__construct` abre a conexão e o canal. **Confirmado rodando o v1:** com o broker parado, `POST` com body inválido (`{}`) e com body válido dão `500` (`stream_socket_client(): Unable to connect to tcp://ms-queues:5672`). O `__destruct` fecha um canal que pode não existir (`Brokers/RabbitMQ.php`; não executado) | alta | **corrigir** | Validar antes de tocar dependência; `202` com broker fora via outbox (ADR 0012) |
| A2 | Persiste a transação e **depois** publica (`TransactionService::process`), sem outbox. **Verificado rodando o v1, e corrigindo a leitura anterior:** com o broker parado desde o início **a transação não é gravada** (a contagem ficou em 62 antes e depois), porque a conexão abre no construtor, antes do `persist`. A inconsistência só existe se o broker cair **entre** o construtor e o `basic_publish` ou se a publicação falhar depois do `persist` (**não testado**). Também não há `Idempotency-Key` | média | **corrigir** | RQ1/RQ2, ADR 0012 |
| A3 | Consumidor com `no_ack=true` (`basic_consume(..., false, true, ...)`): mensagem some se o consumidor cair após a entrega. **Confirmado (O5 do oráculo):** 3 mensagens entregues a um consumidor congelado e perdidas após `kill -9` | alta | **corrigir** | ADR 0004, N2 |
| A4 | Mensagem sem `delivery_mode` persistente (`new AMQPMessage(json_encode(...))`): reiniciar o broker perde as mensagens. **Confirmado (O4 do oráculo):** 2 em *Ready* antes do `docker restart`, 0 depois. O R5 do v1 só vale com o broker de pé | alta | **corrigir** | ADR 0004, RQ5 (mais forte) |
| A5 | Sem *publisher confirms*: `basic_publish` não confirma que o broker recebeu | média | **corrigir** | ADR 0004 |
| A6 | Consumidor **só imprime** (`echo "Printing: ..."`) e não chama o `NotificationService`. O R4 (Loggly) e o consumo não estão ligados (o `specs.md` já declara isso como fora de escopo) | média | **descartar** a lacuna e fechar o fluxo | RQ4: consumidor executa a notificação via `Notifier` (ADR 0020) |
| A7 | Falha do Loggly derruba `/notification/send`: o `LogglyHandler` é o único handler quando há token | média | **corrigir** | ADR 0009 (Loggly descartado), ADR 0020 (falha de destino isolada) |
| A8 | Sem reconexão no consumidor: `while ($channel->is_consuming()) $channel->wait()` morre se a conexão cair | alta | **corrigir** | ADR 0003, N3, RQ5 |
| A9 | Credenciais do broker **fixas no código** em `NotificationCommand` (`'ms-queues', 5672, 'admin', 'admin'`), enquanto `RabbitMQ.php` lê o `.env` | média | **corrigir** | Config única validada no boot (`config check`) |
| A10 | Fila declarada pelos dois lados (`queue_declare` no publicador e no consumidor). Qualquer divergência de argumento dá `PRECONDITION_FAILED` | média | **corrigir** | ADR 0015 (`topology-init` com dono único) |
| A11 | Usa o exchange padrão (vazio) com a fila como *routing key*; sem DLX, retry nem DLQ | média | **corrigir** | ADR 0005 |
| A12 | Sem teste real: só `ExampleTest` do Lumen nos dois serviços (`tests/`) | média | **corrigir** | Estratégia de testes (seção 8 do plano) |
| A13 | Login `admin`/`admin` e `RABBITMQ_DEFAULT_*` no `docker-compose` do broker; portas publicadas em `127.0.0.1` | baixa | **herdar** (só desenvolvimento, já restrito ao loopback) | `.env.example` sem valores; compose local |
| A14 | Um `docker-compose` por serviço com rede externa `ec-network` e `run.sh`/`stop.sh` na raiz | baixa | **herdar** | Fase 1 |
| A15 | Validação do `POST /transaction` antes de qualquer serviço (`payer`/`payee` existentes, `value > 0`); a dependência do broker só entra depois | baixa | **herdar** a ordem | Fase 2 (validar antes de tocar dependência) |
| A16 | `docs/adr/` vazio | baixa | **corrigir** | ADRs 0001 a 0020 |

## Resumo

- 3 pendências do code review do v1 (A1, A7, A3/A4/A8) confirmadas no código; surgiram mais: A2 (consistência banco × fila), A5, A6, A9, A10, A11.
- A **ordem de validação antes da dependência** (A15) já existe no v1 e é herdada.
- Nada do v1 é alterado: ele continua como **oráculo de comportamento** (`oraculo-mensageria.md`).

## Conferência rodando o v1 (2026-11-17)

Confirmados por execução: A1, A3, A4 (e o comportamento de R2, R3 e R5; ver `oraculo-mensageria.md`). **A2 foi corrigido** pela execução (ver a linha). Não executados: A5 a A12 (só leitura do código), o `__destruct` de A1 e a reconexão de A8.
