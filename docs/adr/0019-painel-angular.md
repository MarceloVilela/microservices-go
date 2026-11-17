# ADR 0019: Painel Angular

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** N7, Fase 8 (opcional) (`docs/plan-go.md`)

## Contexto

O painel é opcional, compete com a vaga-alvo em React e só começa com a Fase 7 mergeada.

## Decisão

Em aberto: versão estável vigente do Angular, standalone components e signals, estrutura em `web/` no mesmo repositório, estado (signals ou NgRx), cliente SSE em RxJS, cliente e tipos TypeScript **gerados** do OpenAPI e de `events.schema.json` (G5), estratégia de testes (unidade, componente, ponta a ponta com Playwright). hipótese: **não decidida** até a evidência e a sua aprovação.

## Alternativas

- Frontend em repositório separado.
- React em vez de Angular.
- Sem painel (o backend fecha sem ele).

## Custo

Escopo grande (fase `L`) fora do foco principal do projeto.

## Critério de reversão

Cortar a Fase 8 se o backend consumir o orçamento previsto.

## Evidência

Pendente. Só na Fase 8; depende das ADRs 0016 e 0018.
