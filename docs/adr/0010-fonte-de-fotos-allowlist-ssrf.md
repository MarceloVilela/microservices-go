# ADR 0010: Fonte de fotos, allowlist e SSRF completa

- **Status:** aceita (aprovada pelo usuário em 2026-11-18); o desenho de segurança é **provado na Fase 7**
- **Data:** 2026-11-17
- **Origem no plano:** N4, Fase 7 (`docs/plan-go.md`)

## Contexto

O worker baixa URLs vindas do cliente; allowlist só no worker e só de host deixa SSRF e exaustão de memória em aberto (redirect, DNS rebinding, PNG pequeno com dimensões enormes).

## Decisão

- **Fonte:** Lorem Picsum (confirmado no S3).
- **Allowlist de hosts no ingest (`400`) e no worker.** Hosts: `picsum.photos` e `fastly.picsum.photos` (o destino do redirect).
- **`CheckRedirect`** reaplicando a allowlist a cada salto.
- **Checagem do IP resolvido** a cada conexão e a cada redirect: bloqueia loopback, link-local e redes privadas (contra DNS rebinding).
- Timeout e limite de bytes na resposta.
- **Teto de pixels: 25 megapixels**, verificado com `image.DecodeConfig` **antes** de decodificar.
- Host fora da allowlist, imagem inválida e pixels acima do teto são falha **definitiva** (DLQ direta).
- **Chave de teste de loopback:** os casos 9 e 10 do dataset rodam num servidor local em `127.0.0.1`, que o IP resolvido bloquearia. Para isso existe uma chave de teste que libera loopback e inclui o host do servidor na allowlist. A chave **vem desligada por padrão**, só é lida da configuração de teste e **faz o `config check` falhar** se estiver ligada fora do modo de teste.
- O worker usa `GET` (o `HEAD` devolve 405 no Picsum).

## Alternativas

- Fonte alternativa: Pexels (chave grátis, com rate limit), como fallback.
- Só limite de bytes (não pega PNG pequeno com dimensões enormes; medido no caso 9).
- Allowlist só no worker (o ingest aceitaria qualquer `source_url` e gastaria retries).

## Custo

Resolver e validar o IP a cada conexão e cada redirect é mais código e mais teste. A chave de teste de loopback é uma superfície de risco que precisa ser provada desligada.

## Critério de reversão

O Picsum passar a bloquear ou limitar o uso (rate limit ou mudança de contrato): trocar para o Pexels (reabrir por nova ADR). Rever o teto de pixels se uma fonte legítima passar de 25 Mpx.

## Evidência

- **S3 executado em 2026-11-17** (`specs/spikes/s3-picsum.md`): `302` de `picsum.photos` para `fastly.picsum.photos`, `200 image/jpeg` no destino, `404 Image does not exist` para id inexistente; `HEAD` devolve 405.
- **Dataset** (`specs/dataset-de-paridade.md`, `specs/dataset-server/`): o caso 9 é um PNG válido de 111 KB que declara 30000×30000 (900 Mpx), recusado por `DecodeConfig` sem decodificar (5 KB alocados em teste).
- **Não verificado (fica para a Fase 7, teste de entrada hostil):** redirect para host interno, host que resolve para IP privado, resposta lenta, corpo acima do limite, a chave de teste desligada recusando `127.0.0.1`, e o **rate limit** do Picsum.
