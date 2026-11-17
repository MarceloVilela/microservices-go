# ADR 0017: Armazenamento das miniaturas

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** Fase 6 (`docs/plan-go.md`)

## Contexto

As miniaturas precisam ser gravadas de forma atômica pelo worker, servidas pelo `ms-api` e carregadas por `<img>`, que não envia cabeçalho `Authorization`.

## Decisão

Metadados no Postgres; miniatura em volume local (`/data/thumbs`), nome determinístico por `photo_id`, servida em `GET /photos/{id}/thumb` com `ETag` (`304`) e `Cache-Control`. hipótese: **não decidida** até a evidência e a sua aprovação.

## Alternativas

- MinIO (S3 compatível): serviço a mais, útil se o volume local limitar réplicas.
- Miniatura no banco (`bytea`): infla o Postgres.

## Custo

Volume local exige que worker e `ms-api` compartilhem o mesmo volume.

## Critério de reversão

Querer mais de uma réplica de worker em hosts diferentes: migrar para MinIO.

## Evidência

Pendente. Fase 3 (escrita atômica) e Fase 6 (`ETag`).
