# ADR 0002: Go e estrutura de módulos

- **Status:** aceita (aprovada pelo usuário em 2026-11-18, com a evidência da Fase 1)
- **Data:** 2026-11-17
- **Origem no plano:** D1, D6 (seção 2) (`docs/plan-go.md`)

## Contexto

É preciso decidir a versão do Go e como organizar quatro serviços e código compartilhado sem acoplá-los.

## Decisão

Go na **versão estável vigente** (fixada nesta ADR com a data da conferência); um módulo por serviço, `go.work` na raiz e um módulo `platform` compartilhado. Cada serviço com `cmd/` e `internal/{domain,app,adapters}`.

## Alternativas

- Módulo único com vários `cmd/` (mais simples, mas serviços passam a poder importar uns aos outros).
- Um repositório por serviço (isolamento total, custo alto de CI e de mudança em `platform`).

## Custo

`go.work` e vários `go.mod` aumentam a manutenção; exige teste de arquitetura para impor a regra de dependência (G4).

## Critério de reversão

O spike mostrar fricção que não compense (ex.: CI e Docker com `go.work`), caso em que se volta a módulo único com `depguard`.

## Evidência

Fase 1 (2026-11-18, `specs/fase-1-esqueleto.md`, `specs/execucao-fase-1.log`): `go.work` com 8 módulos (`platform`, quatro serviços,
`ms-queues`, `archtest`, `tests`) e Go 1.27.4 fixado; `make check` e o build das imagens funcionam com `go.work` (`COPY .` + `go build` no Docker);
`archtest` (G4) provado também no caso negativo (import proibido falha o teste). Fricção observada: `./...` na raiz não atravessa módulos
(o `Makefile` itera por módulo) e cada `go.mod` precisa de `replace` para `platform`. `testcontainers-go` ficou em módulo à parte para
não entrar no grafo de `platform`.
