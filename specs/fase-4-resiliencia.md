# Fase 4: Resiliência (G1)

Branch: `fase-4-resiliencia` (a partir da `main` com as Fases 0 a 3). Plano: `docs/plan-go.md` (seção 7, Fase 4).
ADRs exercitadas: 0003 (reconexão), 0004 (ack e confirms), 0005 (escada e `Retry-After`), 0006 (idempotência), 0013 (ordem; fecha com o G1).
Status: **spec aprovada em 2026-11-18** (D1 e D2 decididas pelo usuário conforme a recomendação: D1 = infra consome degrau; D2 = reler estado e aceitar a janela residual). **Implementada; aceite verde** (`specs/execucao-fase-4.log`).

## Ponto de partida (Fase 3, já na `main`)

- Consumidor abre conexão uma vez; se ela cair, `Run` devolve erro e o serviço sai (o compose reinicia).
- Escada: o worker publica no degrau `x-attempt` (0 a 3) e dá ack depois do confirm; esgotada, DLQ + foto `failed`.
- `Retry-After` é ignorado. Erro de banco/disco vira `nack` com `requeue` **sem limite**.
- Relay do outbox e `amqpx.Publisher` já reconectam (backoff 1 a 30 s, conexão preguiçosa); só o **consumidor** não.

## Decisões abertas (precisam do usuário)

| # | Pergunta | Recomendação |
|---|---|---|
| D1 | **Limite do requeue por falha de infraestrutura** (banco/disco): como contar entregas numa fila **clássica** (sem `x-delivery-count`)? | Falha de infra passa a **consumir degrau** como a transitória (mesmo `x-attempt`, mesmo backoff da escada), em vez de `nack requeue`. Esgotada ⇒ DLQ com classe `infrastructure`; a foto fica `pending` se o banco segue fora. Sem fila quorum, sem contador novo |
| D2 | **Ordem DLQ × transação** (achado da Fase 3; a reprocessagem da Fase 6 depende disto) | Manter a ordem (DLQ com confirm, depois a transação), mas **reler o estado** antes de publicar na DLQ e fazer `Finish(failed)` condicional a `pending` (já é). Aceitar a janela residual (foto `done` + mensagem na DLQ só com entregas concorrentes da mesma foto) e o `POST /dlq/{id}/retry` da Fase 6 trata a foto `done` como no-op. Inverter com outbox da DLQ fica como alternativa se você preferir zero janela (mais migração e mais código) |

## Decisões da fase

| # | Decisão | Motivo |
|---|---|---|
| F4-1 | **Reconexão do consumidor** (`consumer.Supervisor`): laço `Run` → em queda de conexão **ou só do canal** (`NotifyClose` nos dois), espera backoff (1 s a 30 s, reinicia o backoff após conexão estável ≥ 30 s) e reabre conexão, canal, `Qos` e `Consume`. Só sai com `ctx` cancelado. Boot com broker fora **espera**, não sai | ADR 0003; critério de aceite que a Fase 1 deixou |
| F4-2 | **Mensagem em voo na queda**: `Ack`/`Nack` que falha por canal fechado **não** encerra o serviço: o broker reentrega e o passo 1 (ADR 0006) absorve. Registrado em log `warn` com `message_id` | idempotência já garante; hoje isso derrubaria o processo |
| F4-3 | **`Retry-After`** (RFC 9110: segundos ou data HTTP) lido em 429 e 503 por `httpfetch`; vai em `domain.Failure.RetryAfter`. Degrau = `max(x-attempt, menor degrau com TTL ≥ Retry-After)`, **arredondado para cima**; acima de 5 min usa o último degrau. 45 s ⇒ `retry.2m`. Valor negativo, ausente ou inválido ⇒ ignora | ADR 0005; aceite do plano |
| F4-4 | **Efeito no contador**: ao pular degraus, `x-attempt` publicado = degrau usado + 1; logo o `Retry-After` alto **gasta** a escada mais rápido (4 degraus no máximo, como hoje). Assumido | simples; sem segundo contador |
| F4-5 | **Infra consome degrau** (D1) e a classe nova `infrastructure` aparece em `x-failure-class`. O `RequeueDelay` de 1 s do consumidor some, salvo para ctx cancelado (devolve sem gastar degrau, como hoje) | evita laço quente e reentrega infinita |
| F4-6 | **Shutdown (N3)**: `SIGTERM` ⇒ para de consumir, espera o voo até `SHUTDOWN_TIMEOUT`, devolve o resto sem ack, fecha publisher e pool, **saída 0**. Mesmo para o `ms-ingest` (HTTP `Shutdown`, relay termina o ciclo, sem linha publicada sem `published_at` além do já tolerado). Testado, não só afirmado | N3 |
| F4-7 | **DLQ com inspeção documentada**: `docs/dlq.md` (como ver mensagens e causa em `x-failure-class` e `x-failure-reason`, sem consumi-las, via painel e via `rabbitmqadmin get ... ackmode=ack_requeue_true`) + `scripts/dlq.sh` | plano da Fase 4 |
| F4-8 | `/readyz` do worker passa a refletir **consumidor conectado** (informativo no corpo; o status HTTP continua dependendo só de topologia, banco e disco, para não reiniciar o serviço por queda breve do broker) | observabilidade da reconexão sem efeito colateral |

Sem variável nova de ambiente além de `CONSUMER_BACKOFF_MAX` (30 s, para os testes usarem valor curto). `config check` a lista.

## Testes (escritos antes do código)

- **Unidade (`-race`)**: leitura de `Retry-After` (segundos, data, lixo, negativo, 0); escolha do degrau (5 s→5s, 45 s→2m, 301 s→último, ausente→`x-attempt`); falha de infra consome degrau e esgota na DLQ com a classe certa; `Supervisor` com dial falso: reabre após queda de conexão, após queda só do canal, backoff crescente e reiniciado, sai com `ctx` cancelado, boot com broker fora; `Ack` com canal fechado não derruba.
- **Integração** (`tests`, testcontainers, binário do worker como black-box):
  - **O4 (R5b)**: lote com worker parado; `docker restart` do broker; `messages_ready` = N depois; worker sobe e termina o lote.
  - **Broker reiniciado com o worker rodando**: lote em andamento, broker reinicia, o **mesmo processo** volta a consumir e o lote termina (sem restart do worker). Idem **só o canal** fechado (`rabbitmqctl close_connection`).
  - **O3 (R5a)** ponta a ponta: parar, publicar, `messages_ready` = N, subir, `0` e todas `done`/`failed`.
  - **Escada ponta a ponta** com `set_policy` de `message-ttl` curto nas filas `retry.*` (a política vence o TTL da fila quando é menor; sem código de produção novo): foto com 503 persistente atravessa os 4 degraus e termina na DLQ com `x-failure-class=exhausted` e a foto `failed`; lote termina.
  - **`Retry-After: 45`** do servidor de teste: a mensagem aparece em `photo.process.retry.2m`.
  - **Id inválido do dataset** termina na DLQ com a causa registrada (aceite do plano).
  - **SIGTERM** com mensagem em voo: termina, ack, saída 0 dentro do prazo (worker e ingest).
- **Aceite (`scripts/aceite-fase-4.sh`)**: reinicia o broker real do compose com o worker no ar, confere O3/O4 e a DLQ inspecionável; `make accept` inclui as Fases 1 a 4.
- **G1**: O1 a O5 do `specs/oraculo-mensageria.md` com os parâmetros declarados (R3 com concorrência 1, `prefetch=1`, um ingest). Resultado do lado Go vai para o fim deste arquivo e para o oráculo.

## Fora do escopo

`ms-notification` (Fase 5); `ms-api`, SSE, reprocessar DLQ (Fase 6); métricas, jitter e carga (Fase 7); poda do outbox (Fase 7); fila quorum.

## Riscos e limites assumidos

- O `eacces` intermitente do RabbitMQ na 1ª partida pode afetar os testes de broker reiniciado: nova tentativa nos scripts e testes (`s1-amqp.md`).
- Sem jitter, o herd da escada continua (ADR 0005, assumido).
- Os TTLs reais (2 e 5 min) não são esperados em teste, só o roteamento para o degrau certo.
- Mudança em `platform/` e nas ordens do worker ⇒ **code-review `high`** antes do PR.

## Fechamento

ADR 0013 vira `aceita` se o usuário aprovar depois do G1; ADR 0003 ganha a evidência de reconexão do consumidor. Atualizar `plan-go.md`,
`CLAUDE.md`, `CLAUDE.local.md`, `README` e o índice de ADRs, e rodar a skill `fechamento-versao`.

## Desvios da spec (implementação)

- **F4-8**: o estado do consumidor ficou em `GET /statusz` (`{"consumer_connected":bool}`), não no corpo do `/readyz` (o corpo do `health.Handler` é fixo e sem detalhe, de propósito). O `/readyz` segue sem depender do consumidor.
- **F4-5**: o `RequeueDelay` de 1 s **ficou**: o `Requeue` agora só ocorre no desligamento (ctx cancelado) e quando o broker não aceita a republicação (degrau ou DLQ); sem o atraso, esse caso viraria laço quente.
- **F4-3**: `Retry-After` limitado a 1 h na leitura (teto contra valor hostil; acima de 5 min já usa o último degrau). Lido só em 429 e 503.
- **Falha de infra após a DLQ** (Finish falhou depois de publicar na DLQ de uma falha definitiva): também consome degrau; a DLQ pode ficar com duplicata (já aceito na Fase 3).
- **Broker reiniciado nos testes de integração**: `rabbitmqctl stop_app/start_app` (O4, mensagens persistem) e o proxy de teste (queda de conexão com o worker no ar). O `docker restart` real, com a mesma porta, está no `scripts/aceite-fase-4.sh`. **Queda só do canal** não tem teste de integração (não há como fechar só o canal de fora); o caminho (`NotifyClose` do canal) fica coberto por leitura e pelo caso de `deliveries` fechado.

## Resultado do G1 (lado Go)

| Cenário | Resultado |
|---|---|
| O1, O2 (FIFO com concorrência 1, `prefetch=1`, um ingest), O5 (kill em cada ponto) | Fase 3, seguem verdes (`make integration`) |
| O3 (R5a): worker parado, `messages_ready` = N, volta e termina | `aceite-fase-3.sh` e `aceite-fase-4.sh` |
| **O4 (R5b)**: broker reiniciado | `TestWorkerO4BrokerReiniciadoComMensagensNaFila` e `docker restart ms-queues` real no aceite 4: 3 em *Ready* antes e depois; o lote termina. O v1 perdia as mensagens |
| Broker reiniciado **com o worker no ar** | mesmo processo reconecta (`TestWorkerReconectaSozinho...`, aceite 4: `StartedAt` do container inalterado) |
| Escada completa, `Retry-After` 45 s ⇒ `retry.2m`, id inválido ⇒ DLQ com causa, SIGTERM ⇒ saída 0 (worker e ingest) | testes de integração e aceite 4 |

G1 fechado no lado Go. ADR 0013 só vira `aceita` com a aprovação do usuário.
