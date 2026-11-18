# ADR 0021: Quarentena de linha venenosa do outbox e identidade da mensagem

- **Status:** proposta (decisões do usuário em 2026-11-18, Fase 3; **aguardando aprovação formal**)
- **Data:** 2026-11-18
- **Origem no plano:** Fase 3 (`docs/plan-go.md`); achados do code-review `high` da Fase 2 (relay para no 1º erro; `message_id` × `MessageId`; coluna `outbox.topic` morta). Complementa a ADR 0012 (aceita, não editada).

## Contexto

O relay da ADR 0012 parava no primeiro erro de publicação: uma linha que o broker rejeita sempre (chave sem fila) travava todo o outbox. Além disso havia duas identidades para a mesma mensagem (`message_id` no payload e `outbox.id` no `MessageId` do AMQP) e uma coluna `topic` que ninguém lia.

## Decisão

1. **Identidade única:** a identidade da mensagem é o `message_id` (UUIDv4) do payload, gerado ao gravar a linha. O relay o usa como `MessageId` do AMQP. `outbox.id` fica interno ao relay (ordem e chave de linha). A coluna `topic` foi removida (migração `0002`).
2. **Quarentena:** colunas `attempts`, `last_error` e `parked_at`. Uma publicação rejeitada **por causa da linha** (`ErrRejected`: mensagem sem rota) soma uma tentativa; ao atingir `RELAY_MAX_ATTEMPTS` (padrão 10) a linha recebe `parked_at` e sai da leitura (`published_at IS NULL AND parked_at IS NULL`). O relay segue com as demais linhas.
3. **Só conta se o ciclo publicou outra linha.** Se **todas** as linhas de um ciclo são rejeitadas, a causa provável é a topologia ausente (broker novo, `topology-init` por rodar), não as linhas: o relay devolve erro (backoff) e **não** conta. Falha de conexão, timeout e nack do broker nunca contam.
4. **Reprocessar** uma linha em quarentena é manual (`UPDATE outbox SET parked_at = NULL, attempts = 0 WHERE id = ...`). Automação, se vier, na Fase 6.

## Alternativas

- Relay que pula a linha sem contar: reprocessa a mesma linha ruim para sempre, com log e custo a cada ciclo.
- Contar toda rejeição: uma topologia ausente levou linhas boas à quarentena em cerca de 5 s (achado do code-review da Fase 3), e nada as devolvia.
- Manter `outbox.id` como identidade do AMQP: duas identidades e nenhuma deduplicação possível no consumidor.

## Custo

- Uma linha venenosa **sozinha** no outbox não entra em quarentena (não trava nada, mas gera um log de erro a cada backoff, até 30 s) até que o mesmo ciclo publique outra linha.
- Quarentena é silenciosa para o negócio: a foto fica `pending` e o lote aberto até alguém reprocessar. Só há log (`error`); alerta e painel ficam para a Fase 7.
- Quatro colunas novas no outbox e uma migração com `DROP COLUMN`.

## Critério de reversão

Se a heurística "algum publicado no mesmo ciclo" gerar quarentena indevida ou deixar linha ruim sem parar na prática, trocar por quarentena com tempo (backoff por linha) ou por classificação explícita do erro do broker.

## Evidência

Fase 3 (`specs/execucao-fase-3.log`): `TestIngestLinhaVenenosaVaiParaQuarentenaESegue` (linha `sem-fila` com linhas boas a cada ciclo: quarentena com `attempts = 3` e `last_error`, as boas publicadas, `/readyz` segue 200) e dois testes de unidade do relay (tudo rejeitado não conta tentativa; a venenosa entra em quarentena no limite). `message_id` chega ao broker como `MessageId` (teste de contrato do ingest). Não testado: topologia apagada com linhas pendentes ponta a ponta (só unidade).
