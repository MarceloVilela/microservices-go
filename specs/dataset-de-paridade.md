# Dataset de paridade

> Entregável 3 da Fase 0. Lote **determinístico** de fotos para exercitar sucesso, retry, DLQ direta e fim de lote
> com falha. Os casos 1 a 7 usam o Lorem Picsum (confirmados no S3, `specs/spikes/s3-picsum.md`); os casos 9 e 10 e
> o cenário de `Retry-After` usam o **servidor local** `specs/dataset-server/` (testado: `specs/dataset-server-teste.txt`).
> **Rascunho para aprovação:** o formato da miniatura e o teto de pixels abaixo são **propostas** (ADR 0010 e 0017),
> não decisões.

## Regras assumidas (a aprovar nas ADRs)

| Regra | Valor proposto | Onde decide |
|---|---|---|
| Miniatura | largura **200 px**, altura proporcional **arredondada** (`round(h × 200 / w)`) | ADR 0017 |
| Teto de pixels (`image.DecodeConfig` antes de decodificar) | **25 megapixels** (hipótese; o caso 9 tem 900 Mpx, então qualquer teto razoável o recusa) | ADR 0010 |
| Foto de 1200×800 → miniatura | **200×133** (133,33 arredondado) | derivado |
| Foto de 600×400 → miniatura | **200×133** | derivado |

## Lote `paridade-1` (9 fotos aceitas pelo ingest)

Hosts: `picsum.photos` (com redirect para `fastly.picsum.photos`, S3) e o servidor local `127.0.0.1:9099` (só em teste; ver "Ressalva do teste local").

| # | Caso | `source_url` | Resultado esperado | Classificação |
|---|---|---|---|---|
| 1-6 | Foto válida, 1200×800 JPEG | `https://picsum.photos/id/{10,20,30,40,50,60}/1200/800` (conferido: `302` → `fastly.picsum.photos`, `200 image/jpeg`, 1200×800, de 57.921 a 158.635 bytes) | `done`, miniatura 200×133 | sucesso |
| 7 | Id inexistente | `https://picsum.photos/id/9999/1200/800` (`404 Image does not exist`) | `failed`, causa registrada, **DLQ direta** | falha definitiva (404) |
| 9 | Teto de pixels | `http://127.0.0.1:9099/big-pixels.png` (PNG **válido** de 111.293 bytes, 1 bit, **30000×30000 = 900 Mpx**; conferido com `file`) | `failed`, **DLQ direta**, **sem decodificar** (só `DecodeConfig`) | falha definitiva |
| 10 | Falha transitória | `http://127.0.0.1:9099/flaky/<chave>` (`503`, `503`, depois `200 image/jpeg` 600×400) | `done` **após 2 retries** (degraus de 5 s e 30 s), miniatura 200×133 | transitória |

(A numeração segue o rascunho anterior; o caso 8 está fora do lote porque o ingest o recusa, ver abaixo.)

## Caso 8, fora do lote: host fora da allowlist

| # | Caso | Requisição | Resultado esperado |
|---|---|---|---|
| 8 | Host fora da allowlist | `POST /batches` com `source_url` = `https://example.com/foto.jpg` | **`400`** no ingest, **nenhum** lote nem job criado. Como a validação é do request inteiro, o caso **não pode** compartilhar o lote `paridade-1`: um `400` rejeitaria as 9 fotos |
| 8b | Mesmo host direto ao worker (mensagem forjada na fila) | publicar `photo.process` com `https://example.com/foto.jpg` | `failed`, **DLQ direta** (host fora da allowlist é falha definitiva no worker também, N4) |

## Resultado esperado do lote `paridade-1`

- **Estado final:** 7 `done` (casos 1 a 6 e 10) e 2 `failed` (casos 7 e 9). Fim de lote com **uma** emissão de `batch.completed`.
- **DLQ:** exatamente os casos 7 e 9, cada um com a causa registrada (`404` e "pixels acima do teto").
- **Caso 10:** a foto fica em retry por ~35 s (5 s + 30 s) antes de `done`; o lote só termina depois disso.
- **Miniaturas:** 7 arquivos de 200×133.

## Cenário extra, Fase 4: `Retry-After`

`http://127.0.0.1:9099/ratelimit/<chave>`: `429` com `Retry-After: 45` na primeira tentativa, `200` na segunda. O aceite da Fase 4 espera que o worker use o degrau de **2 min** (45 s arredondado para cima ao degrau; ADR 0005). Fora do lote `paridade-1` (usa chave própria).

## Servidor local do dataset

`specs/dataset-server/` (Go, módulo isolado, sem dependências): `go run . -addr 127.0.0.1:9099`. Rotas: `/ok.jpg`, `/big-pixels.png`, `/flaky/<chave>` (503 nas 2 primeiras de cada chave), `/ratelimit/<chave>`, `/hits/<chave>` e `/reset` (zera os contadores; chamar antes de cada execução do lote, senão o caso 10 já começa em `200`).

Testes: `go test -race -count=1 ./...` → exit 0, 3 testes (PNG com `DecodeConfig`, sequência 503/503/200 por chave e `/reset`, `429` com `Retry-After: 45`). `DecodeConfig` do PNG alocou **5.280 bytes** (não decodifica a imagem).

## Ressalva do teste local (decisão que a ADR 0010 precisa tomar)

O servidor está em **loopback**, e o plano (N4) manda **bloquear loopback** (e redes privadas) ao checar o IP resolvido. Então os casos 9 e 10 só rodam se existir uma **chave de teste** que libere o loopback e inclua `127.0.0.1:9099` na allowlist. Essa chave deve **vir desligada por padrão**, ser lida só da configuração de teste e **falhar o `config check`** se estiver ligada fora do modo de teste, para não virar um buraco de SSRF. Na Fase 7 o teste de entrada hostil precisa provar que, com a chave desligada, `127.0.0.1` é recusado (classificação: falha definitiva).

## Pendente

- Rate limit e comportamento sob carga do Picsum (não verificados no S3).
- Aprovação do formato da miniatura (200 px de largura) e do teto de 25 Mpx.
- Os tamanhos de arquivo das miniaturas **não** foram fixados (dependem do codificador e da qualidade JPEG; a conferência é por dimensões e decodificação, não por bytes).
- Servidor local testado só em loopback e sem o worker (que ainda não existe).
