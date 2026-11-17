# ADR 0016: ms-api: SSE sobre log de eventos

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** N7, N8, Fase 6 (`docs/plan-go.md`)

## Contexto

O painel precisa de estado ao vivo sem polling e sem ler o broker; reconectar não pode perder nem repetir evento.

## Decisão

Tabela `events(seq bigserial, type, batch_id, photo_id, payload, created_at)` escrita **na mesma transação** de cada mudança de estado; `ms-api` acorda por `LISTEN/NOTIFY` e lê `seq > Last-Event-ID`; `seq` é o `id` do evento SSE. *Heartbeat* a cada ~15 s, limite de buffer por cliente (cliente lento é desconectado e retoma pelo `seq`) e retenção limitada (ex.: 24 h ou N linhas). Cliente que volta depois da janela recebe "recarregue o estado".

## Alternativas

- WebSocket.
- Polling.
- Repassar mensagens do broker ao frontend (descartado: N8).

## Custo

Tabela que cresce (retenção obrigatória); `LISTEN/NOTIFY` exige conexão dedicada.

## Critério de reversão

Carga ou clientes lentos tornarem o SSE inviável; reavaliar WebSocket ou polling.

## Evidência

Pendente. Aceite da Fase 6 (reconectar com `Last-Event-ID`, inclusive após reiniciar o `ms-api`).
