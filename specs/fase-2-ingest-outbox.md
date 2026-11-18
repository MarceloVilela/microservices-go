# Fase 2: `ms-ingest`, Postgres e outbox

Branch: `fase-2-ingest-outbox` (a partir da `main` com as Fases 0 e 1). Plano: `docs/plan-go.md` (seção 7, Fase 2).
Esta branch também leva o conserto do aceite da Fase 1 (`scripts/aceite-fase-1.sh` esperava `healthy` sem repetição e
falhou no CI do PR #2), por decisão do usuário de não abrir PR separado.
ADRs exercitadas: 0007 (Postgres, `pgx`, `sqlc`), 0008 (HTTP), 0012 (outbox e `Idempotency-Key`), 0010 (allowlist no ingest), 0004 (confirms).

## Decisões da fase

| # | Decisão | Motivo |
|---|---|---|
| F2-1 | **`pgx` direto, sem `sqlc`** (a decidir na ADR 0007; aprovação pendente) | o SQL desta fase é pequeno (6 comandos); o `sqlc` adicionaria gerador e passo de CI sem reduzir `Scan` de forma relevante. Reavaliar na Fase 6 (`ms-api` tem muitas consultas de leitura) |
| F2-2 | **Stdlib `net/http` com `ServeMux`** (padrões `POST /batches`), middleware próprio em `platform/httpx` (CORS, token, limite de corpo, `problem+json`) (ADR 0008, aprovação pendente) | cabe em ~150 linhas; critério de reversão: crescer além disso |
| F2-3 | **Migrações** em `platform/db/migrations/*.sql` (embutidas com `embed`), aplicadas pelo `ms-ingest` no boot sob `pg_advisory_lock`. Dono único do esquema na Fase 2; quando o worker/api precisarem, só **verificam** a versão (mesmo princípio da topologia) | evita corrida e esquema divergente |
| F2-4 | Tabelas: `batches`, `photos`, `outbox`, `events`, `schema_migrations`. IDs `uuid` gerados no serviço (`crypto/rand`, UUIDv4) | o `batch_id` volta na resposta `202` sem round trip |
| F2-5 | `Idempotency-Key` opcional, 1 a 128 caracteres `[A-Za-z0-9._-]`. Guardada em `batches.idempotency_key` (`UNIQUE`) com `request_hash` (SHA-256 do corpo canônico). Mesma chave + mesmo corpo: `200` com o mesmo lote (`Idempotent-Replay: true`); mesma chave + corpo diferente: `422` `idempotency_key_reuse` | RFC draft *Idempotency-Key*; concorrência resolvida por `INSERT ... ON CONFLICT DO NOTHING` e releitura |
| F2-6 | Limites: corpo ≤ 1 MiB, 1 a 100 fotos por lote, `source_url` ≤ 2048 bytes, só `https`, sem credencial na URL, host em `ALLOWED_HOSTS` (padrão `picsum.photos,fastly.picsum.photos`) | N4 (ADR 0010); host comparado em minúsculas, porta só 443 |
| F2-7 | Relay: laço com `RELAY_POLL_INTERVAL` (padrão 500 ms) e lote de `RELAY_BATCH_SIZE` (padrão 100). Uma transação por lote: `SELECT ... WHERE published_at IS NULL ORDER BY id LIMIT n FOR UPDATE SKIP LOCKED`, publica cada linha com confirm (`PublishWithDeferredConfirmWithContext`), `UPDATE published_at` e `COMMIT`. Falha no meio: as **já confirmadas** do lote são marcadas e commitadas; a que falhou e as seguintes ficam pendentes (se falhar a primeira, `ROLLBACK`). Erro no commit depois do confirm duplica no ciclo seguinte (*at-least-once*, ADR 0006) | ADR 0012; S4 |
| F2-8 | Publicação **persistente** (`DeliveryMode=2`), `MessageId` = `outbox.id`, exchange `photos`, chave `process`. AMQP `MessageId` = `outbox.id`. Corpo `{"v":1,"message_id":"<uuid>","batch_id","photo_id","source_url"}`, em que `message_id` é um UUID gerado ao gravar a linha do outbox (estável nas republicações, permite deduplicar). Publicação com `mandatory`: mensagem sem rota volta como erro em vez de sumir | contrato em `platform/messages` |
| F2-9 | Conexão do relay com **reconexão e backoff** (1 s a 30 s, com teto) já nesta fase, mínimo para o aceite "broker volta, relay publica tudo". A reconexão completa do consumidor fica na Fase 4 | o aceite da Fase 2 exige |
| F2-10 | `/readyz` do `ms-ingest` = topologia do broker **e** `Ping` no Postgres. Com o broker parado o `POST` ainda dá `202` (só grava) e o `/readyz` dá `503` | plano; ADR 0012 |
| F2-11 | `events`: o `POST` grava `batch.created` (type, `batch_id`, `payload` com `total`) na mesma transação. `NOTIFY` fica para a Fase 6 | plano |
| F2-12 | CORS por allowlist (`CORS_ORIGINS`, vírgulas; vazio = sem CORS) e token de demonstração (`DEMO_TOKEN`, opcional; se definido, exige `Authorization: Bearer`, comparado em tempo constante). `OPTIONS` (preflight) passa sem token | N7 |
| F2-13 | `DATABASE_URL` obrigatória e `Secret`; erro de config nunca ecoa valor | convenção da Fase 1 |

## Esquema (migração `0001_init.sql`)

```sql
batches(id uuid pk, idempotency_key text unique, request_hash text, total int not null,
        completed_count int not null default 0, created_at timestamptz not null default now(), completed_at timestamptz)
photos(id uuid pk, batch_id uuid not null references batches, source_url text not null,
       status text not null default 'pending' check (status in ('pending','done','failed')), created_at timestamptz not null default now())
outbox(id bigserial pk, topic text not null, routing_key text not null, payload jsonb not null,
       created_at timestamptz not null default now(), published_at timestamptz)
  -- índice parcial (id) where published_at is null
events(seq bigserial pk, type text not null, batch_id uuid, photo_id uuid, payload jsonb not null, created_at timestamptz not null default now())
schema_migrations(version int pk, applied_at timestamptz not null default now())
```

## API (`specs/openapi-ingest.yaml`, curto)

- `POST /batches` corpo `{"photos":[{"source_url":"https://picsum.photos/id/10/1200/800"}]}`, cabeçalho opcional `Idempotency-Key`.
  `202 {"batch_id","total","status":"accepted"}` · `200` repetição · `400` `invalid_request` (JSON malformado, lista vazia/grande, URL inválida, host fora da allowlist) · `401` token · `413` corpo grande · `415` não é JSON · `422` reuso de chave.
  Erros em `application/problem+json` com `code` estável.
- `/healthz`, `/readyz` (Fase 1).

## Variáveis novas

`DATABASE_URL` (obrigatória, secreta), `ALLOWED_HOSTS`, `CORS_ORIGINS`, `DEMO_TOKEN` (secreta), `RELAY_POLL_INTERVAL`, `RELAY_BATCH_SIZE`.
`config check` lista os nomes de todas.

## Testes (escritos antes do código)

- Unidade: `httpx` (CORS, token, limite de corpo, `problem+json`), validação do lote (allowlist, URL, limites), `platform/messages` (JSON do contrato), relay com fakes (publica e marca; falha no meio não marca; confirm negado não marca).
- Integração (`tests`, `testcontainers-go`, Postgres 18 e RabbitMQ 4): `POST` grava lote, fotos, outbox e `events` na **mesma** transação; falha injetada no meio não deixa linha parcial; `Idempotency-Key` repetida devolve o mesmo lote (inclusive **concorrente**); chave com corpo diferente dá `422`; relay publica todas e marca; **duas instâncias** do relay sem duplicar nem perder; **broker parado**: `POST` `202`, linhas pendentes, `/readyz` `503`; broker volta: todas publicadas, só do lote certo; corpo inválido `400` sem tocar o banco.
- `scripts/aceite-fase-2.sh` e `specs/acceptance/ingest.http` (RQ7). O teste de integração é *black-box*: compila e executa o binário do `ms-ingest` (`internal/` não é importável de outro módulo) e usa um proxy TCP para "parar o broker" sem perder o estado dele.

## Aceite (do plano) e como se prova

| Critério | Prova |
|---|---|
| lote publicado aparece como *Ready* | aceite: `POST` e fila `photo.process` com N mensagens *Ready* no painel/management API |
| broker parado: `202`, linhas no outbox, `/readyz` falha; ao voltar, relay publica todas | integração + aceite com `docker stop ms-queues` |
| corpo inválido `400` | unidade e integração |
| mesmo `Idempotency-Key` não cria segundo lote | integração (inclui concorrência) |
| RQ2 (publicar sem esperar) | o `POST` responde antes do relay publicar; teste com broker parado |
| `make check`, `make integration`, CI verde | `echo $?` = `0`; CI do PR conferido **antes** do merge |

## Resultado

Evidência em `specs/execucao-fase-2.log`. `make check` e `make integration` com saída `0`; `make accept` (Fases 1 e 2) com saída `0`.
Limites assumidos: o relay faz polling (500 ms por padrão), não `LISTEN/NOTIFY`; o outbox publicado não é podado (Fase 7);
o `RELAY_BATCH_SIZE` e o backoff de 1 a 30 s não foram medidos sob carga (Fase 7).

## Fora do escopo

Worker, `NOTIFY`, reconexão do consumidor, rate limit, autenticação real (token de demonstração apenas), limpeza do outbox publicado (retenção entra na Fase 7).
