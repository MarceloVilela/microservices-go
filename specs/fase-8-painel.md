# Fase 8: painel Angular (opcional)

Branch: `fase-8-painel` (a partir da `main` com as Fases 0 a 7). Plano: `docs/plan-go.md` (seção 7, Fase 8). Plano do painel: `../microservices-frontend-angular/docs/plan-angular.md`.
ADRs exercitadas: 0019 (painel), 0018 (CORS, auth, reprocessamento), 0016 (eventos SSE), 0017 (miniaturas); **0022 adiada pelo usuário para depois do painel** (decisão de 2026-11-19).
Status: **spec aprovada em 2026-11-19** (D1 a D4 aprovadas como propostas pelo usuário). **Implementada em 2026-11-19, aceite verde**; evidência em `specs/execucao-fase-8.log`.

## Escopo por repositório

| Repositório | O que entra |
|---|---|
| `microservices-go` (este) | conserto do aceite 7 (F8-0), publicação do contrato (F8-1), serviço `web` no compose, origem do painel em `CORS_ORIGINS`, aceite 8 |
| `microservices-frontend-angular` | app Angular, cliente gerado do contrato, testes, CI próprio |

## Decisões a aprovar

| # | Decisão | Proposta (recomendada) | Alternativas |
|---|---|---|---|
| D1 | Publicação do contrato entre repositórios (ADR 0019) | **Tag git** neste repositório (`contract-vN`); o painel baixa `specs/openapi-*.yaml` e `events.schema.json` por URL raw da tag, grava em `contract/` e o CI faz `git diff --exit-code` após gerar | submódulo; release com artefato |
| D2 | Estado | **Signals + serviços**; NgRx SignalStore só se a complexidade justificar (ADR-A2 do painel) | NgRx desde o início |
| D3 | Gerador de cliente | **`openapi-typescript`** (tipos) + `fetch` tipado fino; tipos dos eventos via `json-schema-to-typescript` | `openapi-generator` (cliente completo, mais pesado) |
| D4 | Serviço `web` no compose | **nginx** servindo o build estático, imagem construída pelo `Dockerfile` do painel; porta 8080; origem `http://localhost:8080` na allowlist | `ng serve` só em dev (continua possível) |

Pré-requisito de ambiente: Node 24.14 / npm 11.12 já instalados; versão do Angular a fixar no spec do painel via `angular.dev` (v21+ pelo `CLAUDE.local.md` de lá; v20 sai do suporte em 2026-11-28).

## F8-0: conserto do aceite 7 (herdado do PR #8)

O check `images` do PR #8 falhou por `curl | grep -q` sob `pipefail` (SIGPIPE, `curl` sai 23): `http://localhost:8083: sem go_goroutines`, intermitente. **Corrigido** em `scripts/aceite-fase-7.sh` (captura do `/metrics` em variável, `grep -q` sobre ela). Nenhum outro script usa o padrão. Evidência: `specs/execucao-fase-8.log`.

## F8-1: contrato publicado (G5 entre repositórios)

- `specs/openapi-api.yaml`, `specs/openapi-ingest.yaml` e `specs/events.schema.json` são a fonte; tag `contract-v1` na fase.
- O painel **não edita** o contrato; divergência ⇒ o CI dele falha.
- Validação **completa** do `events.schema.json` (hoje estrutural, F6) entra aqui, no lado Go, como teste que valida os eventos reais contra o esquema.

## Painel (repositório irmão; detalhes na spec de lá)

- Standalone, signals, `OnPush`/zoneless, `strict` + `strictTemplates`, `inject()`, `input()`/`output()`, sem `subscribe` solto.
- `HttpClient` com interceptor do token e interceptor de erro mapeando o `code` do `problem+json`; erro de rede ≠ erro de dado; `ErrorHandler` global.
- **SSE** (ADR 0018: `fetch` com `Authorization`, sem token na query) embrulhado em `Observable` com *teardown*, backoff, `Last-Event-ID`, estado "desconectado" visível; trata o evento `reset`.
- Telas: criar lote (normal e "com falhas"), lote em andamento, galeria (cursor, `loading="lazy"`, miniatura com Bearer), DLQ com causa e reprocessar; carregando/vazio/erro em todas; `aria-live="polite"` no progresso e na DLQ; `track` por id.
- Sem SSR (justificar por escrito no README do painel: painel autenticado por Bearer, sem SEO).
- Modo demo sem backend (eventos simulados) do `plan-angular.md`: **fora** desta fase salvo pedido (aceite exige o backend real).

## Este repositório: `web` no compose

- `web/docker-compose.yml` (ou bloco no padrão por serviço), sem build do painel aqui: usa a imagem/contexto do repositório irmão (caminho relativo `../microservices-frontend-angular`), documentado no README.
- `.env.example`: `WITH_WEB=1 ./run.sh` acrescenta `http://localhost:8080` a `CORS_ORIGINS`/`API_CORS_ORIGINS` no `.env` (o padrão dos compose continua só `:4200`); **sem token versionado** (token informado na tela do painel ou injetado em runtime por `config.json`, nunca no build).
- `run.sh`/`stop.sh` sobem/derrubam o `web` só com a flag `WITH_WEB=1` (o backend continua subindo sozinho).

## Testes e aceite

| Camada | O quê |
|---|---|
| Unidade (painel) | serviço SSE com *marble tests*; interceptors; parser de cursor |
| Componente (painel) | cada tela nos 3 estados |
| E2E (Playwright, painel) | "criar lote com falhas → ver na DLQ → reprocessar" contra o compose |
| Este repositório | `scripts/aceite-fase-8.sh`, sempre no `make accept`: origem estranha recusada (403) e contrato = o do painel (pulado se o painel não existir). Com `WITH_WEB=1`: `web` responde 200, CORS do painel aceito, token ausente da imagem (e `WITH_E2E=1` roda o Playwright) |

**Aceite da fase** (do plano): lote criado pelo painel progride ao vivo; foto inválida surge na DLQ com a causa; reprocessar atualiza sem recarregar; derrubar o `ms-api` mostra "desconectado" e, ao voltar, recupera eventos pelo `Last-Event-ID`; sem vazamento de conexões SSE ao navegar (`sse_clients` = base + 1 durante a navegação, nunca uma por tela, e volta à base ao fechar a aba); build de produção sem warning (`echo $?` = 0); E2E verde no CI do painel; `make check`, `make integration` e `make accept` (Fases 1 a 8) com saída 0.

## Limites assumidos

- Foto parada na DLQ (ADR 0022) aparece como hoje; o painel mostra o que `GET /dlq` devolve. Se a 0022 mudar o contrato depois, o cliente é regerado (custo aceito pelo usuário).
- O repositório do painel ainda **não é git**: `git init`, commit e push só a pedido.

## Desvios da spec (o que mudou e por quê)

| # | Desvio | Motivo |
|---|---|---|
| V1 | Aceite "volta a 0" virou "base + 1, e volta à base ao fechar a aba" | o shell mantém **uma** conexão SSE enquanto a aba vive (`Live`); zero só depois de fechar |
| V2 | `Sse` emite `reset` ao reconectar **sem** `Last-Event-ID` | achado ao desenhar o E2E da queda: sem id o servidor só manda o futuro, e o que aconteceu na queda ficaria perdido em silêncio. Teste escrito antes (vermelho) |
| V3 | `openapi-typescript` por `npx` com versão fixada, não como devDependency | peer `typescript ^5` × TS 6 do Angular 22; evita `--legacy-peer-deps` no projeto todo |
| V4 | Node 24.22.3 via `nvm` (default do nvm mudou) | o CLI do Angular 22 exige Node ≥ 24.15; a máquina tinha 24.14.0 |
| V5 | Tag `contract-v1` **não** criada | a tag deve apontar para o commit mergeado; só depois do push (a pedido). `contract/` do painel veio de cópia local |
| V6 | Token único no painel, enviado ao ingest e à API | o ingest só valida `DEMO_TOKEN` se configurado; sem mudar backend nem aceites antigos. Se o ingest passar a exigir token, deve ser o mesmo valor |
| V7 | `events.schema.json` validado por biblioteca completa (`santhosh-tekuri/jsonschema/v6`, módulo `tests`) | era estrutural (F6); agora tipos, formatos e `oneOf`, com teste negativo (6 mutações recusadas) |

## Limites

- CI do painel (`.github/workflows/ci.yml`) **não executado** (diretório sem git; precisa do segredo `BACKEND_READ_TOKEN` e da tag).
- O E2E da queda não distingue recuperação por `Last-Event-ID` de `reset` + REST (os dois têm teste unitário).
- CSP só com `frame-ancestors`; sem auditoria de acessibilidade com leitor de tela; sem modo demo offline.
- Backend CI (`images`) não roda o aceite 8 (precisa do repositório do painel).

## Code review `high` (2026-11-19): 7 achados, todos corrigidos exceto o limite 3

| # | Achado | Correção |
|---|---|---|
| 1 | checagem "token na imagem" passava quando o `docker exec` falhava | exige `docker exec web true` antes; sem container é FALHA |
| 2 | `x="$(curl ...)"` sob `set -e` abortava sem `FALHA` nem resumo (inclusive o `m=` que o F8-0 trouxe ao aceite 7) | `\|\| true` nos pontos de captura; falha de rede vira FALHA reportada |
| 3 | contrato conferido contra a cópia local, não contra a tag; `PANEL_DIR` e `WEB_CONTEXT` independentes | `PANEL` deriva do mesmo `WEB_CONTEXT` do compose; **a comparação com a tag fica como limite** até `contract-v1` existir |
| 4 | `make accept` sem `WITH_WEB=1` não exercitava nada da Fase 8 | o aceite 8 sempre roda (origem estranha + contrato); as seções do `web` ficam atrás de `WITH_WEB=1` |
| 5 | default de CORS ampliado para `:8080` mesmo sem painel (ingest sem token por padrão) | defaults voltam a `:4200`; só `WITH_WEB=1 ./run.sh` acrescenta `:8080` ao `.env` |
| 6 | schema recompilado a cada frame | compilado uma vez e passado à função |
| 7 | `jsonschema/v6` como `// indirect` | `go mod tidy` |
