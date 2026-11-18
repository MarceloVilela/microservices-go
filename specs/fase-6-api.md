# Fase 6: `ms-api`

Branch: `fase-6-api` (a partir da `main` com as Fases 0 a 5). Plano: `docs/plan-go.md` (seção 7, Fase 6).
ADRs exercitadas: 0016 (SSE sobre log de eventos), 0018 (CORS, auth e reprocessamento), 0017 (miniaturas), 0007/0008 (banco, `ServeMux`).
Status: **spec aprovada em 2026-11-18** (D1 a D3 decididas pelo usuário). **Implementada**; evidência em `specs/execucao-fase-6.log`.

## Decisões do usuário

| # | Decisão | Escolha |
|---|---|---|
| D1 | Auth do SSE | **`fetch` com `Authorization: Bearer`**. O servidor **não** aceita token na query |
| D2 | Auth da miniatura | **Bearer sempre** (o painel baixa por `fetch` e usa `blob:` URL). Sem cookie, sem URL pública |
| D3 | `POST /dlq/{id}/retry` | Só reabre foto `failed`; **lote intocado** (`completed_count` não decrementa, `batch.completed` não é reemitido) |

## Endpoints (contrato em `specs/openapi-api.yaml`; erros em `application/problem+json` com `code` estável)

| Rota | Comportamento |
|---|---|
| `GET /batches` | lista por `created_at desc`, cursor opaco (`created_at`,`id`), `limit` 1 a 100 (padrão 20) |
| `GET /batches/{id}` | lote + contadores (`total`, `completed_count`, `failed_count`, `completed_at`); 404 `batch_not_found` |
| `GET /photos?status=&batch_id=&cursor=&limit=` | cursor por `(created_at,id)`; filtros validados (`status` ∈ pending/done/failed) |
| `GET /photos/{id}/thumb` | `image/jpeg` do volume `/data/thumbs` (somente leitura), `ETag` forte (hash do conteúdo), `If-None-Match` ⇒ `304`, `Cache-Control: private, max-age=0, must-revalidate`; foto sem arquivo ⇒ 404 `thumb_not_found` |
| `GET /dlq` | fotos `failed` com `failure_class`, `failure_reason`, `finished_at` (a causa). Cursor como acima |
| `POST /dlq/{id}/retry` | ver abaixo; `202` |
| `GET /events` | SSE (abaixo) |

Todas exigem Bearer (`DEMO_TOKEN`, comparação em tempo constante). `/healthz`, `/readyz`, `/statusz` ficam abertos. Sem `DEMO_TOKEN` configurado o serviço **recusa subir** (diferente do ingest: o ms-api expõe leitura e reprocessamento).

## CORS

Allowlist exata de origens (`CORS_ORIGINS`, lista separada por vírgula; vazia ⇒ nenhuma origem cross-origin). Preflight `OPTIONS` responde `Access-Control-Allow-Headers: Authorization, Content-Type, Last-Event-ID`; origem fora da lista ⇒ sem cabeçalhos CORS (e o `POST` com `Origin` fora da lista ⇒ `403 origin_not_allowed`, não só "sem cabeçalho"). Reusa `platform/httpx` (F6-1).

## SSE (`GET /events`)

1. Requisição sem Bearer ⇒ `401` **antes** de abrir o stream.
2. Ponto de partida: `Last-Event-ID` (cabeçalho) ou, sem ele, o `seq` máximo atual (cliente novo só vê o futuro; o estado inicial vem pelo REST). `Last-Event-ID` inválido ⇒ `400 invalid_last_event_id`; **menor que o menor `seq` retido** (janela de retenção) ⇒ evento `event: reset` com `data: {"reason":"retention"}` e o stream segue do início da janela (cliente recarrega o estado).
3. Cada evento: `id: <seq>`, `event: <type>`, `data: <json do events.payload + batch_id/photo_id>`.
4. Acordar por `LISTEN/NOTIFY` (canal `events`) numa conexão dedicada; **na falta do NOTIFY** (perda, reconexão) um *tick* de segurança de 2 s lê `seq > último`. O NOTIFY só acorda; a verdade é a leitura por `seq`.
5. *Heartbeat*: comentário `: ping` a cada `SSE_HEARTBEAT` (15 s).
6. Cliente lento: escrita com *deadline* (`http.ResponseController.SetWriteDeadline`, 5 s); estourou ⇒ fecha a conexão (o cliente retoma pelo `seq`). Lote de leitura limitado (200 linhas por consulta).
7. Shutdown: `SIGTERM` cancela o contexto raiz dos streams, que enviam `event: shutdown` e fecham; o servidor espera até `SHUTDOWN_TIMEOUT`.
8. Retenção: o `ms-api` poda `events` com `created_at < now() - EVENTS_RETENTION` (24 h) a cada hora. Poda simples por idade; o `reset` (item 2) cobre quem voltar depois.

**Gatilho do NOTIFY**: migração `0004_events_notify.sql` cria trigger `AFTER INSERT ON events` ⇒ `pg_notify('events', seq::text)`. Assim ingest e worker **não mudam**.

## `POST /dlq/{id}/retry`

Numa transação: `UPDATE photos SET status='pending', failure_class=NULL, failure_reason=NULL, finished_at=NULL WHERE id=$1 AND status='failed' RETURNING batch_id, source_url`; se nenhuma linha, ler o estado: não existe ⇒ `404 photo_not_found`; `pending` ou `done` ⇒ `409 photo_not_failed` (no-op; trata a janela residual da D2 da Fase 4 como pedido). Se reabriu: `INSERT INTO outbox` com o `photo.process` **idêntico** ao do ingest, com `message_id` novo (F6-4) e `INSERT INTO events` (`photo.retried`), ambos na mesma transação; `202`.
Efeito: o relay (ingest) publica; o worker processa; resultado `done` ou de volta à DLQ **sem** mexer em `completed_count` (D3). **O worker precisa aceitar reprocessar**: hoje ele fecha com `Finish` condicional a `pending` e conta o lote; reaberta, a foto volta a `pending`, então o `Finish` a fecharia **e contaria de novo**. Por isso `Finish` ganha uma regra (F6-5): só incrementa `completed_count` se a foto **não** tinha `finished_at` anterior ⇒ coluna `photos.attempts_closed int` (migração 0004) incrementada em cada fechamento; contador só sobe quando era 0.

## Decisões da fase

| # | Decisão | Motivo |
|---|---|---|
| F6-1 | CORS e Bearer em `platform/httpx` (já existe no ingest); `ms-ingest` passa a usar o mesmo código, comportamento igual | um só lugar para a segurança de borda |
| F6-2 | Camadas como o worker: `internal/{app,adapters/{postgres,thumbs,sse},httpapi}`; interfaces no consumidor; `pgx` direto, SQL parametrizado | sinais 1, 2, 9 do revisor |
| F6-3 | `ms-api` só lê Postgres e o volume (read-only); **não** consome nem publica no broker. Escreve só no retry (`photos`, `outbox`, `events`) | N8; G4 |
| F6-4 | O retry cria o `message_id` novo (`messages.NewID`) e mantém `photo_id`; a idempotência continua por estado da foto (ADR 0006) | worker deduplica por estado |
| F6-5 | `photos.attempts_closed` + `Finish` do worker só conta o lote quando era a 1ª finalização | D3: lote intocado, sem contar duas vezes |
| F6-6 | Volume `ms-go-thumbs` montado `:ro` no `ms-api` (compose) | ADR 0017 |
| F6-7 | `/readyz`: banco, esquema (versão 4) e existência do diretório de miniaturas; `/statusz`: `sse_clients`, `last_seq` | igual às fases anteriores |
| F6-8 | Contrato conferido no CI: `ms-api/internal/contract` compara as rotas do OpenAPI com as registradas (`httpapi.Routes`, `sse.Route`), resolve todo `$ref` e confere os tipos do `events.schema.json` com os emitidos; o teste de integração valida cada evento visto no SSE contra o esquema | G5 |
| F6-9 | **Buraco de `seq` espera `GapWait` (2 s) antes de ser aceito**: `bigserial` não garante commit na ordem do `seq`; entregar o 6 e seguir perderia o 5 se ele ainda estivesse em voo. Passado o prazo, é rollback | sem isto o SSE pode perder evento sob concorrência |
| F6-10 | `platform/httpx.OriginGuard` (só o ms-api): `Origin` fora da lista ⇒ 403 `origin_not_allowed` em qualquer método, inclusive preflight e SSE. O ingest continua só sem cabeçalhos CORS (comportamento da Fase 2 intocado). O CORS compartilhado ganhou `Last-Event-ID` em `Allow-Headers` | o plano exige "recusadas" |
| F6-11 | Miniatura lida com `os.Root` (nenhum nome escapa do diretório) além da validação de UUID; ETag = SHA-256 do conteúdo | defesa em profundidade |
| F6-12 | `ms-api` usa o token `API_DEMO_TOKEN` (compose) / `DEMO_TOKEN` (processo), gerado pelo `run.sh`, separado do `DEMO_TOKEN` opcional do ingest, para não ligar a auth do ingest sem querer | os aceites 2 a 5 postam no ingest sem token |

## Testes (escritos antes do código)

- **Unidade (`-race`)**: cursor (codifica/decodifica, adulterado ⇒ 400); `app` do retry com fake (reabre `failed`; `done`/`pending` ⇒ 409; inexistente ⇒ 404; outbox e evento na mesma chamada); Bearer em tempo constante, CORS (permitida, fora da lista, preflight); `ETag`/`304`; formatação do SSE (`id`, `event`, `data`, `reset`); worker: `Finish` não conta o lote 2 vezes (teste novo em `ms-worker`).
- **Integração** (`tests`, binário real, Postgres e RabbitMQ em containers): paginação estável com inserções concorrentes (sem repetir nem pular); SSE: `pending → done|failed` visto no stream com o worker real, *heartbeat* em silêncio, `Last-Event-ID` sem perda nem repetição **inclusive depois de reiniciar o `ms-api`**, `reset` fora da janela de retenção, cliente lento desconectado, `SIGTERM` com streams abertos sai 0 no prazo; retry leva uma foto da DLQ a `done` (ou de volta à DLQ) sem alterar `completed_count` e sem segundo `batch.completed`; sem token ⇒ 401 (SSE e thumb incluídos), origem fora da allowlist ⇒ 403; miniatura `304` com `ETag`; boot sem `DEMO_TOKEN` ⇒ saída 1.
- **Aceite (`scripts/aceite-fase-6.sh`)**: pilha real; `curl -N` mostra as transições e `: ping`; reconexão com `Last-Event-ID`; retry de foto da DLQ; `make accept` inclui as Fases 1 a 6.
- **Mutação**: remover o `attempts_closed` ⇒ teste do lote deve falhar.

## Fora do escopo (rastreado)

- Reprocessar linha do outbox em quarentena e e-mail com `last_error` (candidatos à Fase 7).
- Foto `pending` por infra esgotada na DLQ: **não** aparece em `GET /dlq` (que lista `failed`); exigiria ler a fila. Registrado; decidir na Fase 7.
- Painel Angular (Fase 8), métricas (Fase 7), autenticação real (ADR 0018, critério de reversão).

## Desvios da spec

- **Validação do esquema de eventos é estrutural, sem biblioteca de JSON Schema**: o teste de integração confere campos obrigatórios do envelope, o `type` no enum e os campos obrigatórios do `payload` do ramo (`oneOf`). Um validador completo entra com a geração do cliente TypeScript (Fase 8).
- O `ms-api` **não** ganhou `Require` novo no broker: mantém a verificação da DLQ da Fase 1 (o `/readyz` do aceite 1 continua válido); ele não consome nem publica.
- A foto `pending` por infra esgotada continua fora de `GET /dlq` (só `failed`); ver "Fora do escopo".

## Achados do `/code-review high` (2026-11-18)

Corrigidos (teste novo onde cabia):
- **Log de eventos vazio com a sequência avançada** (tudo podado): o 1º evento novo esperava o `GapWait` e um cliente em dia recebia `reset`. `Bounds` agora lê a última `seq` usada da sequência quando a tabela está vazia (`TestAPIPrimeiroEventoDepoisDePodaTotalNaoEspera`; reproduzido antes da correção: 4 s de atraso).
- **`/statusz` do ms-api sem auth** (consulta o banco e expõe estado): passou para os mesmos middlewares das demais rotas.
- **Backoff do `Hub`** nunca voltava a 1 s depois de uma sessão que chegou a escutar.
- **Poll de segurança por cliente** (2 s ⇒ ~N/2 consultas por segundo): subiu para 10 s; o LISTEN é o caminho normal.
- **Deploy com worker antigo** (foto fechada com `attempts_closed = 0` e depois reaberta): `completed_count = LEAST(completed_count + 1, total)`. Efeito colateral: com este teto, remover a guarda `closed > 1` deixa de quebrar o teste do `completed_count` (o `completed_at IS NULL` já impede o 2º `batch.completed`); a guarda segue sendo a regra, o `LEAST` é a rede de segurança.

Refutado: **`ReadTimeout` de 15 s derrubando o SSE**. `TestAPIStreamSobreviveAoReadTimeoutDoServidor` segura o stream por 18 s (só heartbeats) e o evento seguinte chega; mantido como regressão.

Registrados, não corrigidos:
- **Foto `pending` por infra esgotada** não aparece em `GET /dlq` nem aceita retry (409): sem consumidor da fila `photo.process.dlq`. Fase 7 (junto da reconciliação).
- **UUID em maiúsculas** é tratado como inexistente/inválido (o contrato publica minúsculas; `format: uuid` do OpenAPI não obriga). Normalizar se o painel precisar.
- **`OPTIONS /`** responde 405 `problem+json` a qualquer OPTIONS que o CORS não atenda (inclui `/healthz`); inofensivo, simplificar com `OPTIONS` por padrão se incomodar.
