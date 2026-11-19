# ADR 0022: Foto parada na DLQ por infraestrutura esgotada

- **Status:** proposta (aberta no code-review `high` da Fase 7; **sem decisão do usuário ainda**)
- **Data:** 2026-11-19
- **Origem no plano:** Fase 7 (`specs/fase-7-observabilidade.md`, F7-5 e seções do code-review); complementa as ADRs 0005 (DLQ) e 0016 (eventos).

## Contexto

Na Fase 7 a foto cuja infraestrutura (banco, disco) falhou até esgotar a escada vai à DLQ e **segue `pending`**. Para o `ms-api` listá-la em `GET /dlq` e aceitar o retry, o worker grava a marca `failure_class='infrastructure'`, `failure_reason` e `finished_at = now()` (`MarkStalled`, best effort). O predicado "está na DLQ" virou `status='failed' OR (status='pending' AND failure_class='infrastructure')`. Essa solução mínima deixou pontos abertos, vistos nas duas rodadas do review.

## Pontos em aberto (decidir juntos)

1. **Marca best effort:** com o banco fora (a causa mais provável) a marca não grava e a foto fica invisível em `GET /dlq`; o retry dá 409. Caminhos: reconciliador que lê a `photo.process.dlq`, ou regravação com tentativas curtas.
2. **Estado implícito:** `pending` com `finished_at` e `failure_class` preenchidos engana quem lê "terminada" por esses campos. Caminho: estado explícito (`stalled`) ou coluna própria, em vez de reaproveitar os campos.
3. **Sem evento SSE:** a mudança de estado não grava evento, quebrando o invariante "cada mudança de estado, um evento na mesma transação". Caminho: evento `photo.stalled` (muda o contrato `events.schema.json`, G5).
4. **Índice:** o predicado não usa `photos_failed_idx` (parcial em `failed`); `GET /dlq` e o retry varrem `photos`. Exige migração (versão 5) e medida com tabela grande.
5. **Marca não limpa** se um operador reenfileira a mensagem da DLQ por fora (a foto fica em `GET /dlq` e em `photos_stalled` até o `Finish`).
6. **Código 409 `photo_not_failed`** fica enganoso para a foto parada; renomear (ex.: `photo_not_in_dlq`) muda o contrato da Fase 6 (OpenAPI, painel).

## Decisão

Nenhuma ainda. Vale a pena decidir antes da Fase 8 (painel), porque os itens 2, 3 e 6 mudam o contrato que o painel consome.

## Alternativas

- Estado `stalled` explícito no esquema (resolve 2, 3 e 4 de uma vez; custo: migração e mudança de contrato).
- Manter a marca atual e só documentar (custo zero; os itens 1 a 6 ficam como limite conhecido).

## Custo

Qualquer caminho além de "só documentar" exige migração e mudança de contrato entre repositórios (G5).

## Critério de reversão

Se a marca atual não causar confusão na prática (painel e operação usam só `GET /dlq` e a métrica `photos_stalled`), manter e fechar esta ADR como "não fazer".

## Evidência

Fase 7: `TestFotoParadaPorInfraAparecenaDLQERetry`, `TestFalhaAoMarcarNaoTravaNemRepeteADLQ` e a seção "Code review high" de `specs/fase-7-observabilidade.md`.
