# syntax=docker/dockerfile:1
# Imagem única parametrizada: docker build --build-arg PKG=./ms-ingest/cmd/ms-ingest .
# Versão do Go fixada na Fase 1 (ADR 0011); manter igual a go.work.
ARG GO_VERSION=1.27.4

FROM golang:${GO_VERSION}-alpine AS src
WORKDIR /src
COPY . .

FROM src AS build
ARG PKG
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/app ${PKG}

# Diretório das miniaturas, de dono nonroot (uid 65532): o volume nomeado herda esse dono na 1ª montagem
# e o worker roda com read_only (distroless não tem shell para fazer mkdir/chown depois).
FROM src AS thumbs-dir
RUN mkdir -p /out/thumbs && chown 65532:65532 /out/thumbs

FROM src AS build-probe
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/httpsprobe ./tests/cmd/httpsprobe

# Serviço com HTTP: o HEALTHCHECK usa o subcomando do próprio binário (sem curl nem shell).
FROM gcr.io/distroless/static:nonroot AS final
COPY --from=build /out/app /app
COPY --from=thumbs-dir --chown=65532:65532 /out/thumbs /data/thumbs
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD ["/app", "healthcheck"]
ENTRYPOINT ["/app"]

# Comando de execução única (topology-init): sem HEALTHCHECK.
FROM gcr.io/distroless/static:nonroot AS final-job
COPY --from=build /out/app /app
ENTRYPOINT ["/app"]

# Prova de HTTPS na mesma base da imagem final: docker build --target https-probe
FROM gcr.io/distroless/static:nonroot AS https-probe
COPY --from=build-probe /out/httpsprobe /httpsprobe
ENTRYPOINT ["/httpsprobe"]
