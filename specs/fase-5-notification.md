# Fase 5: `ms-notification`

Branch: `fase-5-notification` (a partir da `main` com as Fases 0 a 4). Plano: `docs/plan-go.md` (seção 7, Fase 5).
ADRs exercitadas: 0020 (`Notifier`), 0006 (idempotência), 0003 (reconexão), 0004 (ack manual), 0007 (banco; o `ms-ingest` migra).
Status: **spec aprovada em 2026-11-18** (D1 e D2 decididas pelo usuário conforme a recomendação). **Implementada; aceite verde** (`specs/execucao-fase-5.log`).

## Decisões do usuário

| # | Decisão | Escolha |
|---|---|---|
| D1 | Onde fica a deduplicação por `batch_id` | **Tabela no Postgres** (`notifications`, migração 0003). O `ms-notification` só verifica o esquema (como o worker) |
| D2 | Destino falha (SMTP fora do ar) | **Registrar e dar ack**: log `error` e `last_error` na linha de dedup; sem retry. O consumo nunca para |

## Fluxo por mensagem `batch.completed`

1. `json.Unmarshal` e validação (`batch_id` UUID, `total > 0`, `0 <= failed <= total`). Inválida: log `error`, **ack** (não há DLQ para este evento; reentregar não a conserta).
2. `Store.Once(batch_id, fn)`: transação com `pg_advisory_xact_lock(batch_id)` (serializa entregas concorrentes, ex.: duas réplicas), confere a linha em `notifications`; já notificado ⇒ log `debug`, **ack** (efeito idempotente).
3. Dentro do `Once`, chama **todos** os `Notifier` (slog sempre; SMTP se configurado). Erro de um destino é registrado e **não** impede os demais.
4. Ainda no `Once`: `INSERT` da linha com `last_error` e commit; depois **ack**.
5. Banco fora ou commit falho ⇒ `nack` com requeue após 1 s (infra; só este caso devolve à fila).

Garantia: *at-least-once* na entrega, **efeito único** no caso normal e com réplicas concorrentes. Janela residual assumida: crash do processo (ou falha do commit) entre o passo 3 e o 4 reenvia o e-mail na reentrega. O lock segura uma conexão do banco durante o envio (limitado por `SMTP_TIMEOUT`). Falha do SMTP (D2) não é reenviada: o lote fica registrado como notificado com `last_error`.

## Decisões da fase

| # | Decisão | Motivo |
|---|---|---|
| F5-1 | `Notifier` (`Notify(ctx, messages.BatchCompleted) error`) e a porta de dedup são **definidas no consumidor** (`ms-notification/internal/app`) | ADR 0020; sinal 2 do revisor Go |
| F5-2 | Destinos: `slog` (JSON, sempre) e **SMTP** (`net/smtp`, sem dependência nova), ligado por `SMTP_ADDR`; `SMTP_FROM` e `SMTP_TO` obrigatórios se `SMTP_ADDR` existir (erro no boot). Timeout por envio (`SMTP_TIMEOUT`, 5 s). Sem autenticação nem TLS (Mailpit); e-mail real fora do escopo | ADR 0020 |
| F5-3 | `amqpx.Supervisor` **sobe do `ms-worker` para `platform/amqpx`** (era `consumer.Supervisor`); o worker passa a importá-lo. Os dois serviços reconectam igual (ADR 0003) | dois consumidores: vale compartilhar; `platform` já é o lugar |
| F5-4 | Consumidor próprio do `ms-notification` (uma goroutine, `prefetch=1`, ack manual, `Cancel` e espera do voo no shutdown). **Não** reusa o do worker (é de `internal/`) | G4: serviços não se importam |
| F5-5 | Migração `0003_notification.sql`: `notifications(batch_id uuid PK, notified_at, last_error)`; **sem FK** para `batches` (tabela do serviço) | acoplamento mínimo |
| F5-6 | Compose do `ms-notification` ganha o **Mailpit** (`axllent/mailpit`; SMTP 1025 só na rede interna, UI/API em `127.0.0.1:8025`) | aceite: e-mail chega uma vez no Mailpit |
| F5-7 | `/readyz`: banco, esquema e topologia (como o worker); `/statusz` com `consumer_connected` | igual à Fase 4 (F4-8) |
| F5-8 | `SHUTDOWN_TIMEOUT` e `CONSUMER_BACKOFF_MAX` como no worker; `SIGTERM` ⇒ para de consumir, espera o voo, saída 0 | N3 |

## Testes (escritos antes do código)

- **Unidade (`-race`)**: `app.Service` com fakes: mensagem nova notifica todos e grava; repetida não notifica; um destino falha ⇒ os outros rodam, `last_error` gravado, ack; JSON inválido e `failed > total` ⇒ ack sem notificar; banco fora no `Seen` ou no `Record` ⇒ `Requeue`; ctx cancelado ⇒ `Requeue`. Adaptador SMTP contra um servidor SMTP falso em processo (corpo, remetente, destinatário; recusa e porta fechada devolvem erro, sem pânico). Adaptador slog (campos). Versão de contrato diferente de `messages.Version` ⇒ descartada com log `error`. Erro no `QUIT` do SMTP depois do DATA aceito **não** é falha. `Supervisor` continua com os testes existentes, agora em `platform/amqpx`.
- **Integração** (`tests`, binário real, Postgres e RabbitMQ em containers, SMTP falso em processo):
  - mesmo `batch.completed` publicado **duas vezes** ⇒ **1** linha em `notifications`, **1** e-mail, **1** log de notificação;
  - SMTP indisponível (porta fechada) ⇒ consumo segue, `last_error` preenchido, log `error`, segundo lote também é consumido;
  - reinício do broker com o serviço no ar ⇒ o mesmo processo volta a consumir;
  - `SIGTERM` ⇒ saída 0 no prazo;
  - boot com `SMTP_ADDR` sem `SMTP_TO` ⇒ saída 1.
- **Aceite (`scripts/aceite-fase-5.sh`)**: com a pilha real e o Mailpit, um lote termina, o e-mail chega **uma vez** (API do Mailpit), e republicar o mesmo `batch.completed` na exchange não gera segundo e-mail; `make accept` inclui as Fases 1 a 5.

## Desvios da spec

- `scripts/aceite-fase-3.sh` passou a **parar o `ms-notification`** (religado no `trap` de saída e pelo aceite 5), como o aceite 2 já faz com o worker: ele contava `batch.completed` parado na fila e agora há consumidor.
- `scripts/aceite-fase-5.sh` entrou no job `images` do CI, depois do aceite 3 (o aceite 4 reinicia o broker e segue fora do CI, como antes).

## Achados do `/code-review high` (2026-11-18)

Corrigidos: dedup atômica com advisory lock (corrida entre réplicas; teste de integração com duas réplicas e SMTP lento, mutação confirmada); erro no `QUIT` do SMTP não é falha; versão `v` do contrato conferida; Mailpit fixado em `v1.34.0`; `Ping` redundante no `/readyz`; timer de `ShutdownTimeout` parado ao fim da sessão; `aceite-fase-3.sh` só religa o notification se ele estava de pé e falha se não conseguir pará-lo.

Registrados, não corrigidos:
- **Falha de destino marca o lote como notificado para sempre** (D2 do usuário). Nada reprocessa linhas com `last_error`; candidato à Fase 6 (junto do reprocessamento manual do outbox em quarentena) ou à Fase 7.
- **Consumidor duplicado** entre `ms-worker` e `ms-notification` (Dial, Qos, Consume, `NotifyClose`, shutdown). Extrair um `amqpx.Consume` genérico mexe no worker estável; refatoração própria, depois da Fase 6. O worker tem o mesmo timer de shutdown não parado (inofensivo).
- **Migração 0003 feita pelo `ms-ingest`** (dono único do esquema, ADR 0007): um ingest antigo deixa o notification em `waitSchema` (`/readyz` 503) e todo serviço que verifica o esquema passa a exigir a versão 3. Acoplamento conhecido; revisitar se o esquema for dividido por serviço.

## Fora do escopo

`ms-api`, SSE, reprocessar DLQ (Fase 6); métricas (Fase 7); e-mail real (SPF/DKIM); poda de `notifications`; retry de e-mail.

## Riscos e limites assumidos

- Janela de duplicata descrita acima (crash entre enviar e gravar).
- E-mail de lote cujo SMTP estava fora **não** é reenviado (D2); o `slog` registra o fim do lote de qualquer modo.
- Mudança em `platform/` (Supervisor, migração) e no worker ⇒ **code-review `high`** antes do PR.
