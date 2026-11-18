# ADR 0019: Painel Angular

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** N7, Fase 8 (opcional) (`docs/plan-go.md`)

## Contexto

O painel é opcional, compete com a vaga-alvo em React e só começa com a Fase 7 mergeada.

## Decisão

Em aberto: versão estável vigente do Angular, standalone components e signals, **repositório separado** (`projects-ia/microservices-frontend-angular`, decidido em 2026-11-17), estado (signals ou NgRx), cliente SSE em RxJS, cliente e tipos TypeScript **gerados** do OpenAPI e de `events.schema.json` **publicados por este repositório** (G5 entre repositórios: o contrato é versionado por tag aqui e consumido lá, com `git diff --exit-code` no repositório do painel). Mecanismo de publicação (tag, release ou submódulo) em aberto, estratégia de testes (unidade, componente, ponta a ponta com Playwright). hipótese: **não decidida** até a evidência e a sua aprovação.

## Alternativas

- Frontend em `web/` no mesmo repositório (descartado: o painel é um portfólio Angular isolado).
- React em vez de Angular.
- Sem painel (o backend fecha sem ele).

## Custo

Escopo grande (fase `L`) fora do foco principal do projeto; o contrato entre dois repositórios pode divergir se a publicação não for automatizada.

## Critério de reversão

Cortar a Fase 8 se o backend consumir o orçamento previsto.

## Evidência

Pendente. Só na Fase 8; depende das ADRs 0016 e 0018.
