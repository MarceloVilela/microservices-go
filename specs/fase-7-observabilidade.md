# Fase 7: observabilidade, segurança e carga

Branch: `fase-7-observabilidade` (a partir da `main` com as Fases 0 a 6). Plano: `docs/plan-go.md` (seção 7, Fase 7).
ADRs exercitadas: 0009 (logs e métricas), 0010 (SSRF, prova), 0005 (retry; critério de reversão medido na carga), 0014 (contenção na linha do lote).
Status: **spec aprovada em 2026-11-18** (D1 a D3 decididas pelo usuário; F7-* aprovadas). **Implementada**; evidência em `specs/execucao-fase-7.log` e `specs/execucao-fase-7-carga.log`.

## Decisões do usuário

| # | Decisão | Escolha |
|---|---|---|
| D1 | Métricas | **Prometheus** (`client_golang`, dependência só em `platform/`), `GET /metrics` em cada serviço HTTP |
| D2 | Origem da carga | **Servidor local** `specs/dataset-server` em loopback com a chave de teste (`faultinject`, nunca na imagem). Picsum real só numa amostra pequena (≤ 20 fotos) |
| D3 | Pendências herdadas que entram | **Só** foto `pending` por infra esgotada na DLQ. Ficam fora e rastreadas: poda do outbox publicado, reprocessar quarentena do outbox e e-mail com `last_error`, otimização do `resize` |

## Bloco A: métricas (RQ4, ADR 0009)

`/metrics` em `ms-ingest`, `ms-worker`, `ms-notification` e `ms-api`, **sem Bearer** (como `/healthz`; o compose não publica a porta fora do host). Métricas:

| Métrica | Tipo | Dono |
|---|---|---|
| `photos_processed_total{outcome=done\|failed\|retry\|dlq}` | counter | worker |
| `photo_process_seconds{outcome}` | histograma (baixar + miniatura + transação) | worker |
| `queue_messages{queue}` | gauge (`photo.process`, 4 degraus, `dlq`) | worker |
| `outbox_pending`, `outbox_quarantined`, `outbox_oldest_age_seconds` | gauge (consulta ao Postgres a cada scrape, cache 5 s) | ingest |
| `photos_by_status{status}` | gauge | api |
| `sse_clients`, `sse_last_seq` | gauge | api |
| `http_requests_total{route,code}`, `http_request_seconds{route}` | counter, histograma (rota = padrão do `ServeMux`, **nunca** o caminho cru: cardinalidade) | todos |
| `notifications_total{outcome}` | counter | notification |

Decisões:

| # | Decisão | Motivo |
|---|---|---|
| F7-1 | `platform/metrics` (registro próprio, não o global; *middleware* HTTP e `Handler`) | G4: serviços só importam `platform` |
| F7-2 | `queue_messages` vem de `QueueDeclarePassive` num canal dedicado do worker, com cache de 5 s; falha do broker ⇒ a métrica some (não derruba o scrape) | `ms-api` continua sem tocar o broker (F6-3); não exige o plugin `rabbitmq_prometheus` |
| F7-3 | Sem rótulo de alta cardinalidade (`photo_id`, `batch_id`, URL) | custo do Prometheus |
| F7-4 | `slog`: toda linha de erro do worker/relay carrega `batch_id`, `photo_id`, `message_id` quando conhecidos (conferido por teste) | RQ4 |

## Bloco B: foto `pending` por infra esgotada na DLQ (D3)

Hoje, esgotados os degraus por infra, a mensagem vai à `photo.process.dlq` e a foto **segue `pending`**, invisível em `GET /dlq`, e `POST /dlq/{id}/retry` responde 409.

- **F7-5**: ao mandar à DLQ por infraestrutura esgotada (`domain.Infrastructure`), o worker grava `failure_class='infrastructure'`, `failure_reason` e `finished_at` na foto **sem** mudar `status` nem contar o lote (UPDATE condicional a `status='pending'`, depois de publicar na DLQ). Sem migração: as colunas existem. **Best effort**: se a gravação falhar (o banco fora é a causa provável), loga em `error` e dá ack, em vez de `Requeue`, que faria laço quente com uma duplicata na DLQ por volta.
- **F7-6**: `GET /dlq` passa a listar `status='failed'` **ou** (`status='pending'` e `failure_class='infrastructure'`); o item ganha `status` no contrato (`openapi-api.yaml`, mudança compatível).
- **F7-7**: `POST /dlq/{id}/retry` aceita também a foto `pending/exhausted`: zera `failure_*`, grava outbox (novo `message_id`) e evento, como no `failed`. O worker a conta **uma vez** quando fechar (ela nunca foi contada; `attempts_closed` segue 0), diferente do retry de `failed` (D3 da Fase 6).
- Mutação: remover a gravação do F7-5 ⇒ teste de `GET /dlq` deve falhar.

## Bloco C: teste de entrada hostil (N4, ADR 0010)

Alvo: dataset-server local + `faultinject` (loopback liberado só no teste). Cada caso verifica **a classificação**, e que o worker segue vivo e consumindo logo depois (uma foto válida no mesmo lote termina `done`).

| # | Caso | Camada | Esperado |
|---|---|---|---|
| H1 | URL fora da allowlist | ingest | `400` `host_not_allowed`, nada gravado |
| H2 | URL fora da allowlist chegando direto à fila | worker | definitiva (`failed`, classe `definitive`) |
| H3 | Redirect para host interno/loopback | worker | definitiva (revalida cada salto) |
| H4 | Redirect em laço (> 3 saltos) | worker | definitiva |
| H5 | Host que resolve para IP privado (resolvedor injetado) | worker | definitiva, **sem conectar** |
| H6 | Resposta lenta (acima do timeout) | worker | **transitória** (retry na escada) |
| H7 | Corpo acima do limite de bytes | worker | definitiva |
| H8 | PNG pequeno com dimensões enormes (`/big-pixels.png`) | worker | definitiva por teto de 25 Mpx; **memória do processo não passa de +X MB** (X medido, não inventado) |
| H9 | Não é imagem (HTML com `Content-Type: image/jpeg`) | worker | definitiva |
| H10 | Payload malformado (JSON inválido, `photo_id` não UUID, campo extra enorme) | worker e ingest | ingest `400`; worker: mensagem inválida vai à DLQ sem repetir (não entra em laço de requeue) |
| H11 | Sem token / origem fora da allowlist | api | `401` / `403` (já cobertos na Fase 6; reexecutados aqui como regressão) |

Alguns já têm teste de unidade em `httpfetch`; a Fase 7 acrescenta a **prova ponta a ponta** (ingest → broker → worker → banco → `GET /dlq`) e fecha os "não verificados" da ADR 0010.

## Bloco D: carga (G2, só números reais)

`scripts/carga-fase-7.sh` (+ alvo `make load`, fora do `make accept` padrão por demorar). Dataset-server local, imagens JPEG ~1 MB e PNG pequenos; lotes de 100 fotos até 500 no total.

Medir e registrar em `specs/execucao-fase-7.log` e no `README`:
1. **Vazão** (fotos/s) e tempo total com `WORKER_CONCURRENCY=1` (FIFO, ADR 0013) e com 4 (sem FIFO).
2. **Memória** do `ms-worker` (pico RSS via `docker stats`, amostrado) e **tamanho das imagens** finais (`docker image ls`).
3. **Contenção na linha do lote** (ADR 0014): vazão com 1 lote de 500 × 5 lotes de 100.
4. **Relay**: `outbox_oldest_age_seconds` e backoff sob carga (decide se `RELAY_BATCH_SIZE` muda; só muda com número).
5. **Escada (ADR 0005)**: com `/flaky/` em parte das fotos, contar `429`/erro por rajada de retry (herd). O critério de reversão da ADR 0005 é **avaliado e registrado**, qualquer que seja o resultado.
6. Amostra pequena no Picsum real: só confirma que a classificação de `429` bate; sem rajada.

Sem meta inventada: o log guarda o que saiu, com a máquina (CPU, disco) descrita.

## Testes (escritos antes do código)

- **Unidade (`-race`)**: `platform/metrics` (nomes, rótulos, sem cardinalidade livre); *middleware* usa o padrão da rota; cache de 5 s do `queue_messages`; F7-5 com fake (grava só se `pending`, idempotente); `GET /dlq` e retry com `pending/exhausted` (404/409/202).
- **Integração** (`tests`): `/metrics` dos 4 serviços responde e contém as métricas esperadas depois de um lote; Bloco B ponta a ponta (infra derrubada até esgotar ⇒ aparece em `GET /dlq` ⇒ retry ⇒ `done`, lote conta uma vez); Bloco C (H1 a H10).
- **Aceite** (`scripts/aceite-fase-7.sh`): pilha real; `curl /metrics` em cada serviço; um caso hostil de cada classe; entra em `make accept`. A carga (`make load`) roda uma vez e o log vira evidência.
- **CI**: o `images` roda o aceite 7 (sem a carga); `integration` roda os Blocos B e C.

## Fora do escopo (rastreado)

- Poda do outbox publicado; reprocessar linha em quarentena; e-mail com `last_error` (D3): seguem abertos em `plan-go.md`.
- Otimização do `resize` (`*image.YCbCr`, `ctx` por linha): só se a carga (D2) mostrar custo relevante; aí vira item novo, não escopo desta fase.
- Tracing distribuído, alertas (Alertmanager), dashboards Grafana, rate limit do Picsum em rajada real.
- Validador completo do `events.schema.json` (Fase 8).

## Desvios da spec (apurados na execução)

- **F7-5**: a classe da marca é `infrastructure`, não `exhausted` (a transitória esgotada já fecha a foto como `failed`; só a de infraestrutura a deixa `pending`), e a gravação é *best effort* (ver acima). `finished_at` serve de chave de ordem da DLQ.
- **Métricas**: `photos_processed_total` ganhou os desfechos `skipped` e `requeue` além de `done`, `retry` e `dlq`; `notifications_total` usa `notified`, `destination_failed`, `duplicate`, `invalid` e `requeue`; a idade do outbox se chama `outbox_oldest_pending_age_seconds`; `photos_by_status` só tem `pending`, `done` e `failed` (somam o total); a foto parada é a métrica própria `photos_stalled` (subconjunto de `pending`; corrigido no review).
- **Aceite 7** não repete H2 a H10 do worker na imagem de produção (ela não aceita loopback): ficam nos testes de integração. O aceite cobre as recusas do ingest (H1) e as métricas com o Picsum real.
- **Carga**: o cenário de escada usou o servidor local (sem `429`). A primeira execução travou por bug do **meu teste** (worker do cenário anterior vivo no broker compartilhado, consumindo mensagens de outro banco); cada cenário virou subteste e a execução válida é a registrada.
- **F7-6**: o campo `status` já existia no item; só a descrição do OpenAPI mudou (compatível).

## Limites

- Uma rodada de carga, uma máquina, sem variância; Picsum real não submetido à rajada.
- Foto parada **sem** marca (gravação falhou) segue invisível em `GET /dlq`; só a métrica `photos_processed_total{outcome="dlq"}` e o log a denunciam.
- Sem evento SSE para a foto parada: o painel a vê por `GET /dlq`.

## Code review `high` (2026-11-18)

10 achados. **Corrigidos**: texto do 409 e comentário do `DLQ` desatualizados (o código `photo_not_failed` fica, por ser contrato); `photos_by_status{status="stalled"}` sobrepunha `pending` (virou `photos_stalled`); `ms-notification` importava o client Prometheus direto (virou `platform/metrics.Outcomes`).
**Registrados, não corrigidos** (decisão de escopo, nenhum quebra o aceite):
- **Marca best effort com o banco fora** (o mais importante): na causa mais provável de infra esgotada a foto fica sem marca e invisível em `GET /dlq`, e o retry dá 409. Já constava em Limites. Caminho: reconciliador que lê a `photo.process.dlq` ou grava a marca com tentativas curtas; Fase 9 ou item novo.
- **Índice**: o predicado `inDLQ` não usa `photos_failed_idx` (parcial em `failed`); `GET /dlq`, o retry e a contagem de `stalled` varrem `photos`. Exige migração (versão do esquema 5) e medir com tabela grande.
- **Marca não limpa** se um operador reenfileirar a mensagem da DLQ por fora: a foto fica em `GET /dlq` e em `photos_stalled` até o `Finish`.
- **Sem evento SSE** para a foto parada (a spec já listava como limite).
- **`/metrics` sem Bearer** no mesmo listener (decisão da spec, como `/healthz`): expõe contagens e métricas do runtime a quem alcança a porta; `METRICS_ADDR` separado ou opt-in é o caminho.
- **Gauges do ingest consultam em série** e `sse_last_seq` não tem cache (até ~9 s num scrape com banco degradado). A falha da coleta já é cacheada pelo ttl (corrigido na 2ª rodada).
- **`queue_messages`** abre conexão e canais AMQP a cada refresh, com `context.Background()` e sob o mutex; melhor reaproveitar uma conexão.

### 2ª rodada do `/code-review high` (2026-11-19)

10 achados: 5 repetiam os registrados acima, 5 eram novos.
**Corrigidos (pequenos)**: `Cache` guarda o erro da coleta pelo ttl (`TestCacheGuardaOErroPeloTTL`); `PhotoCounts` em uma só passada (`count(*) FILTER`); `allQueues()` vira a única lista de filas do worker (usada no `Require` e na métrica); `photos_by_status` sem o ttl de 1 s empilhado; `scripts/carga-fase-7.sh` falha com mensagem se o build das imagens falhar e usa o código de saída do `go test` (não só o `grep FAIL`).
**Registrados para depois**:
- **Código 409 `photo_not_failed`** enganoso para a foto `pending` parada: renomear (ex.: `photo_not_in_dlq`) muda o contrato da Fase 6 (OpenAPI, painel, G5); decidir junto com o painel (Fase 8).
- **Índice parcial** para o predicado `inDLQ` (migração 5), `Depths` com conexão reaproveitada, `/metrics` em listener próprio, `MarkStalled` com `finished_at`/evento SSE (ou estado explícito): já registrados acima; todos exigem migração, mudança de contrato ou desenho novo. Reunidos na **ADR 0022** (`docs/adr/0022-foto-parada-na-dlq.md`, `proposta`).
