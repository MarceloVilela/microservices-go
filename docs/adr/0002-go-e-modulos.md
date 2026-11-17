# ADR 0002: Go e estrutura de módulos

- **Status:** proposta
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

Pendente. Montagem do esqueleto na Fase 1 e S4.
