# ADR 0011: Imagem Docker final, versão do Go e healthcheck

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** N5, Fase 1 (`docs/plan-go.md`)

## Contexto

`scratch` não traz certificados CA (o download HTTPS falha com `x509`) nem `curl` para healthcheck.

## Decisão

Imagem final `distroless/static` (tem CA); `scratch` só se esta ADR copiar `ca-certificates` e o teste de download HTTPS passar. Healthcheck por **subcomando `healthcheck` no próprio binário**. Teste de download HTTPS dentro da imagem final já na Fase 1. hipótese: **não decidida** até a evidência e a sua aprovação.

## Alternativas

- `scratch` com `ca-certificates` copiados.
- `alpine` (traz shell e `wget`, imagem maior e mais superfície).

## Custo

`distroless` dificulta depurar dentro do container (sem shell).

## Critério de reversão

Tamanho ou depuração tornarem a imagem impraticável; avaliar `alpine`.

## Evidência

Pendente. S5 (download HTTPS e healthcheck dentro da imagem final).
