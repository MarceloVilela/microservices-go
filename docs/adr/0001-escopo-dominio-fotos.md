# ADR 0001: Escopo: domínio de fotos em vez de transações

- **Status:** aceita (aprovada pelo usuário em 2026-11-17)
- **Data:** 2026-11-17
- **Origem no plano:** D1 (seção 2), S3 (`docs/plan-go.md`)

## Contexto

O v1 (Lumen/PHP) processa transações; o oráculo de dados era 10 usuários e 50 transações (R6). Reescrever o mesmo domínio em Go não acrescenta nada de novo ao portfólio, e o interesse está na mensageria (retry, DLQ, idempotência).

## Decisão

Trocar o domínio para **processamento de fotos** (fonte: Lorem Picsum). A paridade com o v1 passa a ser de **comportamento de mensageria** (R2, R3, R5), não de dados.

## Alternativas

- Manter transações (paridade de dados, mas projeto parece tradução).
- Outro domínio (ex.: PDFs); descartado por não haver ganho sobre fotos.
- Fonte alternativa de fotos: Pexels (chave grátis, com rate limit), como fallback.

## Custo

Quebra o oráculo de dados do v1; exige dataset novo (`specs/dataset-de-paridade.md`) e deixar explícito no README que é reimplementação inspirada.

## Critério de reversão

O S3 mostrar que o Lorem Picsum não serve (contrato ou limites). Primeiro fallback: Pexels; se também falhar, voltar ao domínio de transações.

## Evidência

**S3 executado em 2026-11-17** (`specs/spikes/s3-picsum.md`): `/id/{id}/{w}/{h}` responde `302` para `fastly.picsum.photos` e depois `200 image/jpeg`; id inexistente dá `404 Image does not exist`; os 6 ids do dataset (10, 20, 30, 40, 50 e 60) funcionam em 1200×800. O critério de reversão não foi acionado. **Não verificado:** rate limit e comportamento sob carga do Picsum; se aparecerem, o fallback é o Pexels (reabrir por nova ADR).
