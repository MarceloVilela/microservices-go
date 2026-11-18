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

Pendente. Fase 3 (escrita atômica) e Fase 6 (`ETag`). Proposta de formato no `specs/dataset-de-paridade.md`: largura de 200 px, altura proporcional arredondada (1200×800 e 600×400 viram 200×133).

**Fase 3 (escrita atômica) executada em 2026-11-18:** `filestore.Save` grava em `*.tmp` no mesmo diretório, `fsync`, `rename` e `fsync` do diretório; kill após o temporário e após o `rename` nunca deixou miniatura truncada (`specs/execucao-fase-3.log`). Formato: JPEG, 200 px de largura, altura `round(h×200/w)` (1200×800 e 600×400 viram 200×133), qualidade 85, redimensionamento por média de área só com a stdlib, determinístico. Volume nomeado `ms-go-thumbs` montado em `/data/thumbs` (dono `nonroot` definido na imagem). Temporários órfãos só são apagados no boot se tiverem mais de 1 minuto (protege outra réplica no mesmo volume). Falta a Fase 6 (`ETag`/`304`).