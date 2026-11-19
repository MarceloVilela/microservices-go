# ADR 0005: Retry por degraus (TTL fixo + DLX), DLQ, Retry-After e jitter

- **Status:** aceita (decisão do usuário em 2026-11-17, depois de comparar com o retry nativo da quorum queue)
- **Data:** 2026-11-17
- **Origem no plano:** D4, N1 (`docs/plan-go.md`)

## Contexto

Falha transitória precisa tentar de novo com espaçamento; falha definitiva e esgotamento vão para a DLQ. A documentação do RabbitMQ documenta TTL + DLX, mas recomenda o retry atrasado nativo das quorum queues para este caso (disponível a partir da 4.3). O S2 e o S1 mediram os dois mecanismos no RabbitMQ 4.3.6.

## Decisão

**Escada TTL + DLX:** uma fila de retry por degrau (`photo.process.retry.{5s,30s,2m,5m}`) com **TTL fixo na fila** e DLX de volta para `photo.process`; depois `photo.process.dlq`. `photo.process` e as filas de retry são **clássicas e duráveis**, com mensagens persistentes (ADR 0004). `Retry-After` é arredondado **para cima** ao degrau mais próximo. **Sem jitter** (herd medido e assumido).

## Comparação medida (S1 e S2, RabbitMQ 4.3.6, localhost, uma execução de cada)

| Critério | Escada TTL + DLX (fila clássica) | Retry nativo (quorum) |
|---|---|---|
| Latência de confirm por mensagem (1.000 publicações) | **p50 0,42 ms**, p95 0,72 ms, p99 1,3 ms | p50 5,3 ms, p95 8,5 ms, p99 11,3 ms (~12× mais lento) |
| Throughput com confirm, um por vez | **~2.090/s** | ~173/s |
| Throughput com confirm em lote | **~30.700/s** | ~10.200/s |
| Recuperação após reiniciar o broker (200 publicações, 1 reenvio nos dois) | **29 s** | 67 s (execuções separadas) |
| Atraso do retry | **exato**: 5,003 s e 30,004 s medidos (2 min e 5 min não esperados, só configurados) | linear `min × contagem`: 0,51, 1,14, 1,55, 2,04 e 2,54 s medidos com `min` 500 ms; com `min` 5 s seriam 5 a 25 s (**extrapolado, não medido**) |
| Tempo até a DLQ (4 a 5 tentativas) | 7,6 min (configurado: 5 s + 30 s + 2 min + 5 min) | ~75 s (extrapolado) |
| `Retry-After` por degrau | **sim** (arredondado ao degrau) | **não** (atraso igual para todas as mensagens) |
| Herd (jobs que falharam juntos voltam juntos) | medido: **105 de 200 em 100 ms** | **não medido**; o atraso também é determinístico |
| Jitter | não | não |
| Topologia | 1 fila + 4 de retry + DLQ | **1 fila + DLQ** |
| Quem conta as tentativas | o worker | **o broker** (`x-delivery-count`) |
| DLQ | o worker publica (com a causa) | o broker manda sozinho no `x-delivery-limit`: o worker **não sabe** e o banco ficaria em `pending` |
| Armadilhas medidas | TTL por mensagem em fila única **bloqueia** (a de 1 s esperou a de 6 s): por isso uma fila por degrau | `basic.nack` com requeue **não conta** e fica em loop sem DLQ (~100 entregas em 60 s); queda do cliente consome o orçamento de tentativas (documentação, não testado) |

Fontes: `specs/spikes/s2-retry.md`, `specs/spikes/s1-amqp.md`, `specs/spikes/s1-quorum-resultado.txt`, `specs/spikes/s2-resultado-*.txt`.

## Alternativas

- **Retry atrasado nativo da quorum queue** (`x-delayed-retry-type`/`-min`/`-max` e `x-delivery-limit`): medido acima; descartado pelo usuário, que optou pela escada.
- **Retry no Postgres** (`next_attempt_at` com jitter, aproveitando o relay do outbox): não testado; única opção que resolveria herd e `Retry-After`.
- **Plugin de mensagem atrasada:** não testado.
- **TTL por mensagem em fila única:** descartado (bloqueio de cabeça de fila, medido).

## Custo

- **Sem jitter:** o herd é real (105 de 200 reentregas em 100 ms) e fica como custo assumido para o volume do projeto (centenas de jobs).
- Mais filas e mais topologia (`topology-init`, ADR 0015) e a publicação no degrau feita pelo worker (queda entre a publicação e o ack duplica a mensagem; o worker é idempotente, ADR 0006).

## Critério de reversão

Trocar pelo retry nativo da quorum queue ou por retry no Postgres se, **na carga da Fase 7**, o herd causar `429` ou erros no Picsum (ou em outra fonte), ou se manter a escada se mostrar caro. A evidência do nativo continua disponível acima.

## Evidência

**S2 e S1 executados em 2026-11-17** (tabela acima). **Fase 4 (2026-11-18):** escada completa ponta a ponta (política `message-ttl` curta nas filas `retry.*`): 503 persistente atravessa os 4 degraus e vai à DLQ como `exhausted`; `Retry-After: 45` vai para `retry.2m`; erro de banco/disco consome degrau (classe `infrastructure`, foto fica `pending`). Limites: uma execução de cada, localhost, broker de um nó; a escada real foi esperada só até 30 s (2 min e 5 min são configuração); a quorum foi medida só em latência de confirm e reinício, e o herd do nativo não foi medido; o retry no Postgres não foi testado.

**Fase 7 (2026-11-18), critério de reversão avaliado** (`specs/execucao-fase-7-carga.log`): 200 fotos com 2 falhas 503 cada, 4 workers, degraus reais (5 s e 30 s). As retentativas se agrupam em ondas: **pico de 200 req/s** contra mediana de 8 req/s nos segundos com tráfego (25x), e o total foi o esperado (600 requisições, 0 foto `failed`). Contra o servidor local não houve `429` (ele não limita), então **o critério ("herd causar `429`") não foi disparado, nem refutado**: o Picsum real não foi submetido à rajada. Decisão: manter a escada; a onda de 200 req/s é o número a vigiar e o motivo para, se o Picsum passar a responder `429` em lote grande, trocar por retry no Postgres com jitter.
