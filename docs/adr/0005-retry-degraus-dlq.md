# ADR 0005: Retry por degraus, DLQ, Retry-After e jitter

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** D4, N1 (`docs/plan-go.md`)

## Contexto

Falha transitória precisa tentar de novo com espaçamento; falha definitiva e esgotamento vão para a DLQ. A documentação do RabbitMQ documenta TTL + DLX, mas recomenda o retry atrasado nativo das quorum queues para este caso (versão mínima e compatibilidade com a imagem do broker não conferidas).

## Decisão

Hipótese de partida: uma fila de retry por degrau (`photo.process.retry.{5s,30s,2m,5m}`) com **TTL fixo na fila** e DLX de volta, depois `photo.process.dlq`. `Retry-After` arredondado **para cima** ao degrau mais próximo. Sem jitter nesse mecanismo (assumido).

## Alternativas

- Retry atrasado nativo das quorum queues.
- Retry no Postgres (`next_attempt_at` com jitter, reaproveitando o relay do outbox).
- Plugin de mensagem atrasada.
- TTL por mensagem em fila única: descartado (bloqueio de cabeça de fila).

## Custo

Sem jitter, jobs que falharam juntos voltam juntos (*thundering herd*); aceito para o volume do projeto, **medido** no S2.

## Critério de reversão

O S2 mostrar *thundering herd* relevante ou o retry nativo das quorum queues ser viável: trocar por ele, por retry no Postgres ou por plugin.

## Evidência

Pendente. S2 (ordem, backoff, DLQ, herd e disponibilidade do retry nativo na versão do broker).
