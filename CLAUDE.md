# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Regras gerais do workspace (limite de 100 linhas, sem commit/PR/push sem pedido, sem co-autor do Claude, notas locais em `CLAUDE.local.md`) estão em `/home/marcelo/Desktop/coding/CLAUDE.md` e valem aqui. Este arquivo deve ficar com no máximo 100 linhas.

## Estado atual

**Não há código, `go.mod`, Makefile nem CI.** O diretório só tem documentação de planejamento em `docs/` (e `docs/adr/` vazio). Não existem comandos de build/lint/teste ainda; qualquer comando abaixo é **alvo do plano**, não algo que rode hoje. Não há repositório git neste diretório.

Os documentos são escritos em português (idioma do projeto; manter).

- `docs/plan-go.md`: plano de implementação (spec-driven), **rascunho para aprovação**. Fonte principal: status das fases, requisitos RQ/N, gates G0 a G5, arquitetura alvo, fases 0 a 9, decisões em aberto.
- `docs/plan-go-code-review.md`: review `high` do plano (11 achados) e rastreio das correções já aplicadas em `plan-go.md`.
- `docs/zgo.md`: transcrição da conversa que escolheu o projeto (reescrita em Go do `ec-microservices-rabbitmq`) e o modelo de documentação (`published/php-laravel`).

## O que o projeto é

Reimplementação em Go (inspirada, **não fiel**) do v1 `projects-ia/ec-microservices-rabbitmq` (Lumen/PHP + RabbitMQ + MySQL). O domínio muda de transações para **pipeline de fotos** (fonte: Lorem Picsum). O que permanece como oráculo é o **comportamento de mensageria** (R2, R3, R5 do v1). O v1 deve ficar intocado. Painel Angular (Fase 8) é opcional.

## Arquitetura alvo (resumo; detalhes em `plan-go.md` seção 6)

Serviços: `ms-ingest` (HTTP + outbox + relay), `ms-worker` (consome, baixa, gera miniatura), `ms-notification` (consome `batch.completed`), `ms-api` (leitura, SSE, reprocessar DLQ), mais o RabbitMQ 4 (`ms-queues`) e Postgres. Módulo compartilhado `platform/` (config, `slog`, AMQP com reconexão, tipos de mensagem); serviços **nunca importam uns aos outros** (gate G4).

Fluxo: `POST /batches` grava lote, fotos e linhas do **outbox na mesma transação** e responde `202`; um relay (`FOR UPDATE SKIP LOCKED`) publica `photo.process` com publisher confirm; o worker processa com ack manual e escreve estado + contador do lote + evento + (se for o último) outbox de `batch.completed` **numa só transação**, e só então dá ack.

Pontos que exigem ler vários trechos para entender:

- **Retry**: filas `photo.process.retry.{5s,30s,2m,5m}` com TTL fixo e DLX de volta, depois `photo.process.dlq`. Falha transitória (timeout, 5xx, 429) faz retry; definitiva (404, imagem inválida, host fora da allowlist, pixels acima do teto) vai direto à DLQ **e conta como foto terminada**. Sem jitter (assumido; ADR 0005 pode trocar por quorum queue delayed retry).
- **Topologia com dono único**: só o comando `topology-init` declara exchange/filas; os demais serviços verificam com `QueueDeclarePassive` e falham `/readyz` se divergir.
- **Ordem do worker por mensagem**: checa estado no banco → baixa e gera miniatura em arquivo temporário → `fsync` + `rename` atômico → transação no banco → ack.
- **Ordem FIFO (R3)** só vale com `WORKER_CONCURRENCY=1` e `prefetch=1` (ADR 0013).
- **SSE do painel** vem de uma tabela `events` com `seq` (escrita na mesma transação de cada mudança de estado, `LISTEN/NOTIFY`); `seq` é o `Last-Event-ID`. Nunca de polling nem de mensagens do broker.
- **Segurança de download (N4)**: allowlist no ingest e no worker, `CheckRedirect`, checagem do IP resolvido, limite de bytes, `image.DecodeConfig` antes de decodificar.
- Imagem final `distroless/static`; subcomando `healthcheck` no binário (sem `curl`).

## Convenções do plano (obedecer ao evoluir os docs)

- Requisitos usam prefixo **RQ** (e **N** para novos); gates usam **G0 a G5**. Nunca misturar ids; toda referência usa o prefixo completo.
- Metodologia: spec (`specs/fase-N-nome.md`) → branch `fase-N-nome` → teste antes do código → evidência em `specs/execucao-fase-N.log` → PR (só a pedido) → merge antes da fase seguinte. Um PR por fase.
- ADRs em `specs/adr/` com Status, Contexto, Decisão, Alternativas, Custo e **Critério de reversão**; nenhuma vira `aceita` sem evidência (spike medido) e aprovação do usuário. Itens marcados *(hipótese)* só viram decisão via ADR.
- Gates declaram o código de saída esperado; conferir `echo $?` **sem pipe**; scripts com `set -euo pipefail`.
- Definição de pronto por fase (alvo): `go build ./...`, `go vet ./...`, `go test -race ./...`, `golangci-lint run`, `govulncheck ./...` com saída `0`; teste de arquitetura (G4); CI reproduzido em container limpo.
- Referências cruzadas entre docs devem ser conferidas contra arquivos reais (note que `plan-go.md` cita `zangular.md`, `specs/` e `CLAUDE.md` que não existem neste diretório).
- Sem deploy real (fora de escopo); sem segredo versionado (`.env.example` sem valores).
