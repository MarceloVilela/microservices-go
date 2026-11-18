# ADR 0011: Imagem Docker final, versão do Go e healthcheck

- **Status:** aceita (aprovada pelo usuário em 2026-11-18); a versão do Go e o `Dockerfile` multi-stage real são fixados e testados na Fase 1
- **Data:** 2026-11-17
- **Origem no plano:** N5, Fase 1 (`docs/plan-go.md`)

## Contexto

`scratch` não traz certificados CA (o download HTTPS falha com `x509`) nem `curl` para healthcheck.

## Decisão

- **Imagem final `gcr.io/distroless/static:nonroot`** (traz o bundle de CA e roda como usuário não root).
- **Healthcheck por subcomando `healthcheck` no próprio binário** (sai com 0 ou 1), usado no `HEALTHCHECK` do Dockerfile e no `healthcheck` do compose; sem `curl`, sem shell.
- **Teste de download HTTPS dentro da imagem final já na Fase 1.**
- **Versão do Go:** a estável vigente na Fase 1, fixada no `go.mod` e no `Dockerfile` naquele momento (o S5 usou a 1.27.4). Esta ADR não a fixa.
- `scratch` com `ca-certificates` copiados é alternativa **testada** (9,4 MB), mas exige manter o bundle de CA atualizado de propósito no build.

## Alternativas

- `scratch` com `ca-certificates` copiados.
- `alpine` (traz shell e `wget`, imagem maior e mais superfície).

## Custo

`distroless` não tem shell, o que dificulta depurar dentro do container (usar `docker debug` ou uma imagem de depuração à parte). ~6,4 MB a mais que o `scratch` com CA.

## Critério de reversão

Tamanho ou depuração tornarem a imagem impraticável: avaliar `scratch` com CA ou `alpine`.

## Evidência

**S5 executado em 2026-11-17** (`specs/spikes/s5-imagem.md`): `scratch` sem CA falha com `x509: certificate signed by unknown authority`; `scratch` com CA copiado (9,4 MB) e `distroless/static:nonroot` (15,8 MB) baixam HTTPS com sucesso, inclusive seguindo o redirect para `fastly.picsum.photos`; o subcomando `healthcheck` fez o `HEALTHCHECK` chegar a `healthy` em 4 s, sem `curl` nem shell, e sai com 1 sem servidor. **Limites:** o binário foi compilado no host e copiado (o `Dockerfile` multi-stage real, com a imagem `golang`, não foi testado); os tamanhos são de um binário de teste de 6 MB; o healthcheck cobre só `/healthz`, não o `/readyz`.
