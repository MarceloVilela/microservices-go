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

FROM src AS build-probe
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/httpsprobe ./tests/cmd/httpsprobe

# Serviço com HTTP: o HEALTHCHECK usa o subcomando do próprio binário (sem curl nem shell).
FROM gcr.io/distroless/static:nonroot AS final
COPY --from=build /out/app /app
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
