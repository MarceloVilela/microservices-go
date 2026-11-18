# Fase 3: `ms-worker` e fim de lote

Branch: `fase-3-worker` (a partir da `main` com as Fases 0 a 2). Plano: `docs/plan-go.md` (seção 7, Fase 3).
ADRs exercitadas: 0004 (ack e confirms), 0005 (retry), 0006 (idempotência), 0010 (SSRF), 0013 (ordem), 0014 (fim de lote), 0017 (miniaturas).
Status: **spec aprovada pelo usuário em 2026-11-18; implementada, aceite verde** (resultado no fim).

## Pendências da Fase 2 resolvidas aqui (decisões do usuário, 2026-11-18)

| # | Decisão | Efeito |
|---|---|---|
| P1 | **Ordem do outbox: restringir o FIFO.** R3 vale como "ordem de chegada ao broker", com um `ms-ingest` e `WORKER_CONCURRENCY=1`, `prefetch=1`. Sem advisory lock; `SKIP LOCKED` e escala mantidos | ADR 0013 registra a divergência do v1 (bigserial ≠ ordem de commit). O cenário de R3 do G1 declara esses parâmetros |
| P2 | **Linha venenosa: tentativas + quarentena** (formalizada na ADR 0021). Migração `0002`: `outbox.attempts int`, `outbox.last_error text`, `outbox.parked_at timestamptz`. Após `RELAY_MAX_ATTEMPTS` (padrão 10) falhas de publicação da **mesma** linha, ela recebe `parked_at` e sai da fila (`WHERE published_at IS NULL AND parked_at IS NULL`); o relay segue com as demais. Log `error` e métrica no `/readyz`? **Não**: só log (readiness não pode depender de linha ruim) | o relay deixa de travar no 1º erro; reprocessar uma linha em quarentena é manual (documentado), automação fica para a Fase 6 |
| P3 | **Identidade da mensagem = `message_id` (UUID do payload).** O `MessageId` AMQP passa a ser esse UUID. A coluna morta `outbox.topic` é removida na `0002` | uma só identidade para deduplicar; `outbox.id` fica interno ao relay |

## Decisões da fase

| # | Decisão | Motivo |
|---|---|---|
| F3-1 | `ms-worker` em `ms-worker/internal/{domain,app,adapters}`. Portas **definidas no consumidor** (`app`): `Photos` (estado e transação), `Fetcher`, `Thumbnailer`, `Store` (arquivo), `Broker` (ack/nack/republica). Adapters: `postgres`, `picsum`/`httpfetch`, `thumbs`, `amqp` | RQ8, interface no consumidor; teste de unidade com fakes |
| F3-2 | Consumo: `prefetch = WORKER_PREFETCH` (padrão 1), `WORKER_CONCURRENCY` goroutines (padrão 1), **ack manual**. Cada goroutine tem dono (`errgroup`) e termina com o `ctx`; no shutdown para de consumir, espera as mensagens em voo (prazo `SHUTDOWN_TIMEOUT`) e **não** dá ack de nada incompleto | N3, ADR 0004 |
| F3-3 | **Ordem por mensagem** (do plano): (1) lê `photos.status`; `done`/`failed` ⇒ só ack; (2) baixa e gera a miniatura em `*.tmp` dentro de `THUMBS_DIR`; (3) `fsync` do arquivo e do diretório + `rename` para `THUMBS_DIR/<photo_id>.jpg`; (4) **uma transação**: `UPDATE photos SET status=... WHERE id=$1 AND status='pending'` (0 linhas afetadas ⇒ já feito, só ack), `UPDATE batches SET completed_count = completed_count + 1 ... RETURNING completed_count, total`, se `completed_count = total` e `completed_at IS NULL` ⇒ `completed_at = now()` + linha `batch.completed` no outbox, e linhas em `events` (`photo.done`/`photo.failed`; `batch.completed` se for o caso); (5) ack | ADR 0006 e 0014. A guarda `status='pending'` na própria `UPDATE` é o que impede dupla contagem sob redelivery e sob duas fotos terminando juntas (a linha do lote serializa) |
| F3-4 | **Miniatura**: JPEG, largura 200 px, altura proporcional arredondada (1200×800 ⇒ 200×133), qualidade fixa, redimensionamento determinístico (mesma entrada ⇒ mesmos bytes, para conferir contra o esperado). Nome determinístico por `photo_id`; `*.tmp` órfão é apagado no boot | `specs/dataset-de-paridade.md`; ADR 0017 |
| F3-5 | **Download seguro (N4, ADR 0010)**: allowlist de hosts (`ALLOWED_HOSTS`, a mesma do ingest), só `https`, porta 443, `CheckRedirect` revalidando a cada salto (máx. 3), **IP resolvido** checado no `DialContext` (bloqueia loopback, privado, link-local, metadata) para cobrir DNS rebinding, `http.Client.Timeout` (`FETCH_TIMEOUT`, padrão 10 s), `io.LimitReader` (`MAX_IMAGE_BYTES`, padrão 10 MiB), `image.DecodeConfig` **antes** de decodificar, teto `MAX_PIXELS` (padrão 40 Mpx), só JPEG/PNG (decoders registrados explicitamente) | N4; entrada hostil é teste da Fase 7, mas as defesas nascem aqui |
| F3-6 | **Classificação de falha**: *transitória* (timeout, erro de rede, 5xx, 429) ⇒ **republica no degrau de retry** (ver F3-7); *definitiva* (4xx exceto 429, imagem inválida, host fora da allowlist, IP bloqueado, pixels acima do teto, redirect inválido) ⇒ passo 4 com `failed`, **conta como foto terminada**, `events` guarda a causa e a mensagem vai à DLQ com cabeçalhos `x-failure-class` e `x-failure-reason`. Falha do **banco** (passo 1 ou 4) é transitória de infraestrutura: `nack` com `requeue` (sem consumir degrau) | N6, ADR 0005; o endurecimento da escada e do `Retry-After` é a Fase 4 |
| F3-7 | **Escopo do retry na Fase 3**: o worker publica no degrau seguinte, usando o cabeçalho `x-attempt` (0 a 3), e dá ack da original **depois** do confirm; esgotados os 4 degraus, a mensagem vai à DLQ **e** a foto fecha como `failed` (mesma transação do passo 4). A escada em si (TTL + DLX) já existe da Fase 1. Backoff do consumidor, `Retry-After` e roteiros de R5 ficam na **Fase 4** | o aceite da Fase 3 exige um lote com foto inválida que termina; transitório completo é Fase 4 |
| F3-8 | **DLQ**: a publicação na DLQ é um publish com confirm feito **antes** do passo 4 e do ack. Se o processo morrer depois do publish e antes do ack, a mensagem original é reentregue e a DLQ pode ter **duplicata** (at-least-once, ADR 0006); o passo 1 absorve (foto já `failed` ⇒ só ack) | consistência com o resto; duplicata na DLQ documentada |
| F3-9 | Worker **não migra** o esquema: só **verifica** a versão (`schema_migrations` ≥ a esperada) no boot e no `/readyz`, junto com a topologia e o `Ping` no Postgres (F2-3) | dono único do esquema |
| F3-10 | `/readyz` do worker = topologia (já existe) + Postgres + `THUMBS_DIR` gravável. O volume `thumbs` é compartilhado com o `ms-api` (Fase 6) no compose | ADR 0017 |
| F3-11 | `message_id` do payload vira o `MessageId` AMQP e a chave de log (`slog`: `message_id`, `batch_id`, `photo_id`, `attempt`) | P3, ADR 0009 |

## Migração `0002_worker.sql`

```sql
ALTER TABLE outbox DROP COLUMN topic;
ALTER TABLE outbox ADD COLUMN attempts int NOT NULL DEFAULT 0,
                   ADD COLUMN last_error text,
                   ADD COLUMN parked_at timestamptz;
DROP INDEX outbox_pending_idx;
CREATE INDEX outbox_pending_idx ON outbox (id) WHERE published_at IS NULL AND parked_at IS NULL;
ALTER TABLE photos ADD COLUMN failure_class text, ADD COLUMN failure_reason text, ADD COLUMN finished_at timestamptz;
```

`ms-ingest` é ajustado junto (deixa de gravar `topic`, passa a usar `message_id` como `MessageId` AMQP). Mudança em `platform/` e no
esquema ⇒ **code-review `high`** antes do PR.

## Variáveis novas

`WORKER_CONCURRENCY` (1), `WORKER_PREFETCH` (1), `THUMBS_DIR` (`/data/thumbs`), `FETCH_TIMEOUT`, `MAX_IMAGE_BYTES`, `MAX_PIXELS`,
`ALLOWED_HOSTS` (compartilhada), `SHUTDOWN_TIMEOUT`, `RELAY_MAX_ATTEMPTS` (ingest). `DATABASE_URL` já existe (secreta).
`config check` lista os nomes de todas.

## Testes (escritos antes do código)

- **Unidade (fakes, `-race`)**: classificação de falha; passo 1 (já terminada ⇒ só ack); ordem dos passos (ack só depois do commit); `rename` só
  depois do `fsync`; falha do banco ⇒ `nack` com `requeue`; degrau e `x-attempt`; DLQ + `failed` ao esgotar; SSRF (allowlist, redirect, IP
  bloqueado, bytes, pixels, `DecodeConfig`); miniatura (dimensões, determinismo); relay com linha venenosa (quarentena após N, demais seguem).
- **Integração** (`tests`, `testcontainers-go`, Postgres 18 + RabbitMQ 4; worker é **black-box**, binário compilado, como o ingest):
  - lote do dataset termina; miniaturas conferidas contra o esperado (dimensões e hash);
  - **kill em cada ponto**: (a) após o `*.tmp`, (b) após o `rename`, (c) após o commit e **antes do ack** ⇒ reentrega, sem duplicar resultado,
    sem miniatura truncada, `completed_count` correto; o ponto é forçado por **hook de teste** (variável `WORKER_TEST_KILL_AT`, compilada só com a
    tag `faultinject`, nunca na imagem final);
  - lote com **foto inválida** termina e emite `batch.completed` **uma vez**;
  - **duas fotos terminando ao mesmo tempo** ⇒ um só `batch.completed` (repetido sob `-race` e com concorrência 4);
  - redelivery da mesma mensagem N vezes ⇒ um só efeito; `events` sem duplicata de `photo.done`;
  - R2 e R3 (concorrência 1, `prefetch=1`, um ingest) conforme `specs/oraculo-mensageria.md`; G1 verde nesses cenários.
- `scripts/aceite-fase-3.sh` e `specs/acceptance/worker.http` (RQ7). `make accept` passa a rodar as Fases 1, 2 e 3.

## Aceite (do plano) e como se prova

| Critério | Prova |
|---|---|
| lote do dataset processado, miniaturas conferidas | integração + aceite |
| matar o worker nos 3 pontos reentrega sem duplicar nem truncar | integração com `faultinject` |
| lote com foto inválida termina e emite `batch.completed` uma vez | integração |
| duas fotos finalizadas ao mesmo tempo não geram dois eventos | integração (`-race`) |
| G1: R3 (concorrência 1) e R2 | integração contra `oraculo-mensageria.md` |
| pendências P1 a P3 fechadas | ADR 0013 (divergência), migração `0002`, teste de quarentena |
| `make check`, `make integration`, `make accept`, CI verde | `echo $?` = `0` sem pipe; CI conferido **antes** do merge |

## Fora do escopo

Backoff e reconexão do consumidor, `Retry-After`, roteiros R5 e "broker reiniciado" (Fase 4); `ms-notification` (Fase 5); `ms-api`, SSE,
`NOTIFY` e reprocessar DLQ (Fase 6); poda do outbox, carga, rate limit do Picsum e teste de entrada hostil (Fase 7).

## Resultado

Evidência em `specs/execucao-fase-3.log`: `make check`, `make integration` e `make accept` (Fases 1, 2 e 3, com o Picsum real) com saída `0`.
Foram 9 testes de integração do worker (lote com 4 falhas definitivas, kill nos 3 pontos, 15 lotes terminando juntos com concorrência 8,
reentrega repetida, FIFO com concorrência 1, retry no degrau de 5 s, `/readyz` sem diretório, esperar o esquema) e 1 do relay (quarentena).
Um teste de mutação (remover a guarda `status = 'pending'`) fez o teste de reentrega falhar, como deveria.

Desvios da spec, todos pequenos:
- **Redimensionamento só com a stdlib** (média por área em aritmética inteira, determinística) em vez de `golang.org/x/image/draw`: sem dependência nova.
- **Temporários órfãos** são apagados no boot só se tiverem mais de 1 minuto (F3-4 dizia "no boot"): protege o temporário de outra réplica no mesmo volume.
- **Esquema:** além de verificar no `/readyz`, o consumo **espera** o esquema (em vez de falhar o boot), para não reentregar à toa.
- **Prefetch efetivo** = `max(WORKER_PREFETCH, WORKER_CONCURRENCY)`; com 1 e 1 vale o modo oráculo.
- **Reconexão do consumidor:** não existe (Fase 4). Se a conexão cair, `Run` devolve erro e o serviço sai; o compose reinicia.
- `WORKER_TEST_KILL_ONCE` (marcador de arquivo) existe na build `faultinject` mas os testes atuais não o usam: reiniciam o worker sem o kill.
- O `aceite-fase-2.sh` agora **para o `ms-worker`** no início (senão ele consumiria as mensagens que o aceite conta como Ready); o aceite 3 o religa.

Limites assumidos: kill em processo único (réplicas concorrentes sobre a mesma foto não foram exercitadas); `Retry-After` ignorado (Fase 4);
o degrau de retry foi exercitado com o 503 de uma tentativa (a escada inteira e o esgotamento só têm teste de unidade, Fase 4 cobre ponta a ponta);
a inversão do `bigserial` do outbox não foi medida; carga e contenção na linha do lote, Fase 7.

## Riscos e perguntas em aberto

- **Aprovação pendente (usuário):** ADR 0008 (`ServeMux`) e `sqlc` não adotado (ADR 0007), herdadas da Fase 2.
- `WORKER_TEST_KILL_AT` exige build tag `faultinject`: (resolvido) `archtest` impede Dockerfile, compose e `run.sh` de mencioná-la; `make vet` e `make lint` compilam com ela.
- (resolvido) redimensionamento sem dependência nova, ver "Resultado".

## Code-review `high` (2026-11-18): 9 achados

Corrigidos na própria Fase 3 (com teste):
- **Mensagem com id que não é UUID** travava a fila (erro de banco vira `Requeue`): `parse()` agora valida UUID e a manda à DLQ como definitiva.
- **Quarentena do outbox** punha linhas boas em quarentena quando a topologia sumia: só conta a rejeição se o mesmo ciclo publicou outra linha; tudo rejeitado devolve erro (backoff) e não conta. Consequência assumida: uma linha venenosa **sozinha** não entra em quarentena (não trava nada, mas gera log de erro a cada backoff).
- **Altura da miniatura** sem limite (1×25.000.000 passava no teto de pixels): `MaxHeight` = 2000 (falha definitiva "proporção da imagem extrema").
- **DNS** que não resolve passou a ser transitório (o host é da allowlist); `httpfetch.New` não altera mais o slice do chamador.
- **`stop_grace_period: 30s`** no compose do worker (o `SHUTDOWN_TIMEOUT` de 10 s + download de 10 s estouravam os 10 s do Docker).
- **Aceite 2** religa o worker por `trap` ao sair; **aceite 3** espera a DLQ crescer em vez de comparar uma vez.

Ficam para depois (complexos; riscos assumidos):
- **Requeue sem limite** para erro de banco persistente em `Status`/`Finish` (ex.: erro SQL determinístico que não seja de UUID): precisa de contador de entregas (quorum queue `x-delivery-count` ou cabeçalho) e decisão de DLQ. **Fase 4** (resiliência).
- **DLQ × foto já `done`**: a publicação na DLQ vem antes da transação (F3-8), então, com entregas concorrentes da mesma foto, ela pode terminar `done` e ainda ter mensagem na DLQ. Corrigir exige inverter a ordem (transação primeiro, DLQ depois, com outbox) ou reler o estado. **Decidir antes da Fase 6** (o reprocessar da DLQ depende disso).
- **Desempenho do `resize`**: leitura pixel a pixel por interface e sem checar cancelamento; com 25 Mpx e concorrência alta custa CPU e memória. Otimizar por tipo (`*image.YCbCr`) e checar `ctx` por linha: **Fase 7** (medir primeiro; G2).
