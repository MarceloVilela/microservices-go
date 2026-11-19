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
- **Fase 3 (2026-11-18), worker:** `httpfetch` aplica allowlist, só `https`, porta 443, sem credencial na URL, `CheckRedirect` revalidando cada salto (máx. 3), checagem do **IP resolvido** na conexão (`net.Dialer.Control`: loopback, privado, link-local, multicast, CGNAT e IPv4 mapeado em IPv6), sem proxy de ambiente, prazo total, limite de bytes (`Content-Length` e leitura limitada) e `image.DecodeConfig` com teto de pixels antes de decodificar. Testes de unidade cobrem cada regra e provam, com servidor TLS em loopback, que o IP é recusado mesmo com a allowlist satisfeita. No fluxo ponta a ponta, host fora da allowlist, 404, imagem inválida e pixels acima do teto vão à DLQ e contam como terminadas. As chaves de teste (loopback e HTTP) só existem na build `faultinject`; `archtest` impede que Dockerfile, compose ou `run.sh` as mencionem. **Continua para a Fase 7:** teste de entrada hostil contra o binário de produção, DNS rebinding real e rate limit do Picsum.

**Fase 7 (2026-11-18), entrada hostil de ponta a ponta** (`tests/hostil_integration_test.go`, `specs/execucao-fase-7.log`): ingest recusa com `400` e **sem gravar nada** host fora da allowlist, IP literal interno, metadados da nuvem (`169.254.169.254`), `http`, `file`, credencial na URL, host por sufixo (`picsum.photos.evil.com`), mais de 100 fotos, JSON malformado, URL gigante e corpo de 2 MiB. No worker, cada caso termina na classe esperada e a foto válida do mesmo lote segue `done`: host fora da allowlist e credencial ⇒ `permanent`; **redirect para host interno** ⇒ `permanent` (`host fora da allowlist`); redirect em laço ⇒ `permanent` (`redirects demais`); corpo acima do limite ⇒ `permanent`; **PNG pequeno com dimensões enormes** ⇒ `permanent` (`pixels acima do teto`) com **+244 kB** de pico de memória do worker (decodificar custaria ~140000 kB); HTML como imagem ⇒ `permanent`; **resposta lenta** ⇒ transitória, percorre a escada inteira (5 buscas) e termina `exhausted`. **Host que resolve para IP interno** (`localhost` e `127.0.0.1` na allowlist, worker sem o modo de teste): `endereço IP bloqueado` sem conectar; porta fora de 443 ⇒ `porta não permitida`. Mensagem malformada (JSON quebrado, UUID inválido, versão desconhecida, campo de 1 MiB, foto inexistente) vai à DLQ como definitiva, **sem laço de reentrega**. Não verificado: rate limit do Picsum real.
