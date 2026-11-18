# S5: imagem final (download HTTPS e healthcheck sem `curl`)

Executado em 2026-11-17. Código em `specs/spikes/s5-imagem/` (`main.go`, três Dockerfiles; o binário e o bundle de CA em `ctx/` não são versionados); saída bruta em `specs/spikes/s5-resultado.txt`.

**Ambiente:** Go 1.27.4, binário estático (`CGO_ENABLED=0`, `-trimpath -ldflags "-s -w"`, 6,35 MB) com os subcomandos `serve`, `download URL` e `healthcheck`, compilado **no host** e copiado para a imagem (não testei o `Dockerfile` multi-stage com `golang:` para não baixar ~1 GB). Imagem base `gcr.io/distroless/static:nonroot` (6,65 MB, baixada). Download real contra `https://picsum.photos/id/10/200/200`.

## Resultados

| Imagem | Tamanho (`docker images`) | Download HTTPS |
|---|---|---|
| `scratch` **sem** CA | 9,11 MB | **falha**: `tls: failed to verify certificate: x509: certificate signed by unknown authority` (exit 1) |
| `scratch` **com** `ca-certificates.crt` copiado do host | 9,41 MB | **ok**: `status=200 final=fastly.picsum.photos tipo=image/jpeg bytes=6488` |
| `distroless/static:nonroot` | 15,8 MB | **ok**: mesmo resultado, seguindo o redirect para `fastly.picsum.photos` |

| Healthcheck (imagem distroless, usuário `nonroot`) | Resultado |
|---|---|
| `HEALTHCHECK CMD ["/app","healthcheck"]` com o servidor no ar | `starting` em 2 s e **`healthy` em 4 s** |
| `docker exec <c> /app healthcheck` | `healthy`, exit 0 |
| `healthcheck` sem servidor | `unhealthy: ... connection refused`, **exit 1** |
| `docker run --entrypoint sh` | falha: `exec: "sh": executable file not found` (sem shell, como esperado) |

## Conclusões (para a ADR 0011)

- **Confirmado o achado 8 do code review do plano:** `scratch` sem CA quebra todo download HTTPS com `x509`, que na regra do plano viraria falha "transitória" e iria para retry e DLQ.
- **Subcomando `healthcheck` funciona** como substituto do `curl`, tanto no `HEALTHCHECK` do Dockerfile quanto no `docker exec`; o código de saída reflete a saúde.
- **As duas opções viáveis funcionam:** `distroless/static` (15,8 MB, CA incluído, usuário `nonroot`) e `scratch` + CA copiado (9,4 MB). Diferença de ~6,4 MB; `distroless` traz o bundle de CA atualizado pela imagem base, enquanto no `scratch` o bundle é do host de build (precisa ser atualizado de propósito).
- O download seguiu o redirect `picsum.photos` → `fastly.picsum.photos` sem ajuste, mas **não** havia `CheckRedirect` nem checagem de IP neste spike (isso é a ADR 0010 e a Fase 7).

## Limites desta evidência

- O binário foi compilado no host e copiado; o **`Dockerfile` multi-stage real** (stage `golang`) não foi testado.
- Tamanhos vêm de `docker images` (conteúdo da imagem), com um binário de teste pequeno; o binário real dos serviços será maior.
- `scratch` testado com o bundle de CA **do host**; não comparei com o bundle da própria imagem `golang`.
- Não testei `HEALTHCHECK` com `/readyz` (broker e banco), só `/healthz`.
- Só `linux/amd64`; uma execução.
- O trecho "sem shell" saiu com `exit=0` no log por causa do `head` no pipe; o erro do runtime é o que vale.
