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

Fase 8 (2026-11-19), `specs/fase-8-painel.md` e `specs/execucao-fase-8.log`: Angular 22 zoneless com signals; contrato por tag git (`contract-v1`) com tipos gerados (`openapi-typescript` fixado por `npx`, por causa do peer `typescript ^5`; `json-schema-to-typescript` para os eventos); SSE por `fetch` com `Authorization` embrulhado em `Observable` (teardown, backoff, `Last-Event-ID`); nginx sem root servindo o build estático. 37 testes de unidade e componente, lint, build sem warning e 4 testes ponta a ponta contra o compose. **Aguardando aprovação** (a tag ainda não foi publicada e o CI do painel não rodou).
