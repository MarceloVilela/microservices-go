# ADR 0006: Idempotência do worker

- **Status:** **aceita** (2026-11-18, aprovada pelo usuário; evidência da Fase 3)
- **Data:** 2026-11-17
- **Origem no plano:** N2, Fase 3 (`docs/plan-go.md`)

## Contexto

A entrega é *at-least-once* (redelivery, duplicata do relay entre confirm e marca), então processar duas vezes a mesma foto não pode duplicar resultado.

## Decisão

Chave `photo_id` + `batch_id`; **fonte da verdade é o banco**. Ordem por mensagem: checar estado (`done`/`failed` ⇒ só ack) → baixar e gerar miniatura em arquivo temporário → `fsync` e `rename` atômico para nome determinístico por `photo_id` → transação (estado, contador, evento, outbox) → ack.

## Alternativas

- Deduplicação por `message_id` em cache (perde estado ao reiniciar).
- Confiar na existência do arquivo (arquivo truncado seria tratado como pronto).

## Custo

Uma leitura no banco por mensagem e escrita em duas etapas (arquivo e banco).

## Critério de reversão

Contenção de lock ou custo medido tornar a checagem inviável; rever a chave ou a ordem.

## Evidência

Pendente. Testes que matam o worker nos três pontos (após o temporário, após o `rename`, após o commit e antes do ack).

**Fase 3 executada em 2026-11-18** (`specs/fase-3-worker.md`, `specs/execucao-fase-3.log`): o binário real do `ms-worker` foi morto com `SIGKILL` (build `faultinject`) em cada ponto: `after_tmp` (sem arquivo definitivo, foto `pending`), `after_rename` (miniatura inteira, foto `pending`) e `after_commit` (foto `done`). Nos três a mensagem voltou à fila (sem ack), o worker reiniciado terminou o lote e o resultado ficou único (`completed_count` 1, um `photo.done`, um `batch.completed`, miniatura 200×133 decodificável). Reentrega da mesma mensagem 6 vezes teve um só efeito. A idempotência está na guarda `status = 'pending'` do `UPDATE`: removê-la (teste de mutação, registrado no log) faz o teste falhar com `completed_count = 4`. Limite: o kill é em processo único; duas réplicas concorrentes sobre a mesma foto não foram testadas (a guarda vale para elas, mas não foi exercitada).