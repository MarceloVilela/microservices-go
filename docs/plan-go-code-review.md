# Code review do `plan-go.md` (nível `high`)

> Alvo: `plan-go.md` (versão anterior à aplicação das correções).
> Origem: agente `code-review`, nível `high`, 2026-11-17. O agente **não viu** o `zangular.md`; o achado 7
> (frontend Angular) é verificação do assistente por `grep` (nenhuma ocorrência de `ms-api` ou `Angular` no plano).
> Os números de linha são da versão revisada, que mudou depois das correções.
> Veredito: `CONFIRMED` = verificado contra o texto do plano; `PLAUSIBLE` = depende de contexto não verificado.

## Pedido

revisa o plano go com codereview high, considerando que agora este projeto vai ter um frontend angular @zangular.md

## Achados (do mais grave ao menos grave)

### 1. Banco e fila sem consistência (linha 194, `CONFIRMED`)

A Fase 2 grava o lote no Postgres e só depois publica no broker, sem outbox, compensação nem chave de idempotência no `POST`.

**Cenário:** `POST /batches` com 50 fotos grava 50 jobs. O broker cai depois de 20 confirms e a API responde 503. O banco guarda 50 jobs pendentes, só 20 foram publicados e nada republica os outros 30. Se o cliente repetir o `POST`, cria um segundo lote. O plano nunca define a ordem commit/publish, nem o estado resultante do 503.

### 2. Fim de lote sem dono e "exatamente um log" impossível (linha 136, `CONFIRMED`)

O `batch.completed` não tem dono nem mecanismo, e o aceite "exatamente um log" contradiz a entrega *at-least-once*.

**Cenário:** com N goroutines, réplicas e redelivery, nada detecta o fim do lote (sem contador atômico no Postgres) nem diz se foto na DLQ conta como terminada. Se não conta, um lote com um id inválido (que o dataset inclui de propósito) nunca emite o evento. Duas goroutines que terminam as últimas fotos juntas podem emitir dois eventos, ou nenhum. O `ms-notification` não deduplica, e a fila de `batch.completed` não está na topologia da Fase 1.

### 3. G1 exige FIFO (R3), mas o desenho não preserva ordem (linhas 63 e 116, `CONFIRMED`)

O v1 diz "consome a fila, em ordem FIFO". O gate G1 e o aceite da Fase 3 exigem "G1 verde nos cenários de R3", mas `prefetch` com N goroutines e reenvio via retry não preservam ordem.

**Cenário:** o roteiro de R3 do oráculo falha por construção, ou G1 é aprovado porque o cenário foi reescrito em silêncio. O plano não registra a divergência como ADR, embora a definição do G1 peça isso.

### 4. Jitter e `Retry-After` são incompatíveis com filas de TTL fixo (linha 88, `CONFIRMED`)

O plano exige "backoff com teto e jitter; respeitar `Retry-After`", mas a D4 usa filas de retry com TTL fixo (5 s, 30 s, 2 min).

**Cenário:** sem jitter, os jobs que falharam juntos voltam juntos (*thundering herd*). Um `Retry-After` de 45 s cai no degrau de 30 s e a requisição volta cedo. TTL por mensagem em fila compartilhada sofre bloqueio de cabeça de fila, que o próprio plano admite nos riscos. A D4 e a lição são incompatíveis, e o plano não diz qual cede.

### 5. N4 (SSRF e exaustão de memória) está incompleto (linha 75, `CONFIRMED`)

A mitigação cobre só host, timeout e bytes.

**Cenário:**
- (a) `picsum.photos` redireciona para `fastly.picsum.photos` e o `http.Client` segue redirects por padrão. Sem `CheckRedirect` reaplicando a allowlist, um redirect para `169.254.169.254` ou um host interno passa. O IP resolvido também não é checado (DNS rebinding).
- (b) A allowlist roda só no worker. O ingest aceita qualquer `source_url`, e "host fora da allowlist" não é falha definitiva (DLQ direto), então gasta retries.
- (c) Limite de bytes não protege contra um PNG pequeno que declara dimensões enormes. Falta `image.DecodeConfig` com teto de pixels antes de decodificar. O teste "imagem gigante" da Fase 6 pode passar só porque o limite de bytes pega o caso fácil.

### 6. Ordem arquivo → banco → ack indefinida (linha 202, `CONFIRMED`)

A Fase 3 exige ack manual depois de persistir e "gravação idempotente", mas não define a ordem entre gravar a miniatura, atualizar o banco e dar ack, nem a atomicidade da gravação.

**Cenário:** o worker grava `/data/thumbs/x.jpg` e morre antes do commit e do ack. Na redelivery a idempotência olha o banco, não vê resultado e reprocessa. Sem escrita temporária com `rename` atômico, uma morte no meio da escrita deixa uma miniatura truncada que a checagem pode tratar como pronta. O teste de aceite ("matar o worker no meio de um job") cobre um único ponto de falha.

### 7. O plano não incorpora o frontend Angular nem o `ms-api` (linha 18, `PLAUSIBLE`)

**Cenário:** sem `ms-api`, `GET /jobs`, `GET /photos`, `POST /dlq/{id}/retry` e o stream de eventos não têm dono, fase, ADR (SSE, WebSocket ou polling; armazenamento das miniaturas) nem aceite. O reprocessamento da DLQ também conflita com a idempotência (N2), e nenhum gate cobre CORS, autenticação do painel ou contrato OpenAPI compartilhado com o frontend.

### 8. Imagem `scratch` sem certificados CA nem healthcheck (linha 89, `CONFIRMED`)

**Cenário:** sem `/etc/ssl/certs`, todo download de `https://picsum.photos` falha com `x509: certificate signed by unknown authority`, vira falha "transitória" e cai em retry e DLQ. Sem `curl` nem `wget`, o `healthcheck` do compose contra `/healthz` e `/readyz` não roda, a menos que o binário tenha um subcomando próprio. O problema só aparece na Fase 3, depois da ADR 0011.

### 9. `internal/topology` compartilhado, sem ADR e sem dono único (linha 140, `CONFIRMED`)

**Cenário:** a Fase 1 diz que `internal/` compartilhado só existe "onde houver ADR", mas nenhuma das ADRs 0001 a 0011 trata de pacote compartilhado. Com `go.work` e módulos separados, o pacote compartilhado exige versionamento, e a visibilidade de `internal` depende do path do módulo raiz. Se cada serviço declara a topologia no boot, uma divergência de argumentos (`x-message-ttl`, `x-dead-letter-*`) entre versões de imagem causa `PRECONDITION_FAILED` e derruba o serviço que subir por último.

### 10. IDs G1 a G8 dos requisitos colidem com os gates G0 a G4 (linha 61, `CONFIRMED`)

**Cenário:** o aceite da Fase 2 diz "G2 de mensageria" (requisito), mas o gate G2 é "Eficiência". A Fase 3 diz "G1 verde nos cenários de R3", misturando os dois. Um gate pode ser marcado como fechado cobrindo só o requisito de mesmo número.

### 11. Justificativa da D5 (Postgres) frágil e em conflito com a decisão em aberto 3 (linha 51, `PLAUSIBLE`)

**Cenário:** a D5 diz que Postgres tem "melhor suporte em `testcontainers-go`", afirmação não medida (há módulo oficial para MySQL também). O resto do plano (S4, G1, Fase 2) já pressupõe Postgres; se a decisão 3 (SQLite) mudar, a ADR 0007, os gates e os testes precisam ser reescritos.

---

## Rastreio: onde cada achado foi tratado no `plan-go.md`

Aplicado em 2026-11-17, a pedido do usuário. Nenhuma ADR foi aceita; as novas (0012 a 0018) nascem como previstas na Fase 0.

| # | Achado | Correção no plano |
|---|---|---|
| 1 | Banco × fila sem consistência | **Outbox** (RQ1, RQ2, seção 6, Fase 2), `Idempotency-Key` no `POST`, ADR 0012; com broker fora do ar o ingest responde `202` e o relay publica depois |
| 2 | Fim de lote sem dono | **N6**, contador atômico e `batch.completed` via outbox na mesma transação (Fase 3), foto na DLQ conta como terminada, dedup por `batch_id` no `ms-notification` (Fase 5), ADR 0014; "exatamente um log" virou "efeito idempotente" |
| 3 | FIFO × concorrência | RQ3 e G1: FIFO só com `WORKER_CONCURRENCY=1` e `prefetch=1`; divergência na ADR 0013; o oráculo declara os parâmetros |
| 4 | Jitter × TTL fixo | D4 reescrita: degraus de TTL fixo, `Retry-After` arredondado para cima, **sem jitter** assumido e medido no S2; a ADR 0005 prevê a troca de mecanismo |
| 5 | SSRF incompleta | **N4** completo (allowlist no ingest e no worker, `CheckRedirect`, IP resolvido, `image.DecodeConfig`); host fora da allowlist vira falha definitiva (N1); Fase 7 testa a classificação |
| 6 | Ordem arquivo → banco → ack | Passos 1 a 5 da Fase 3 (temporário, `fsync`, `rename`, transação, ack); teste de morte em três pontos |
| 7 | Sem frontend Angular nem `ms-api` | Fases 6 (`ms-api`, SSE, DLQ) e 8 (painel, opcional), ADRs 0016 a 0018, gate **G5** de contrato, decisões 5 e 6 |
| 8 | `scratch` sem CA nem healthcheck | `distroless/static` por padrão, subcomando `healthcheck` (N5), S5 e teste de download HTTPS na imagem final já na Fase 1 (ADR 0011) |
| 9 | Topologia sem dono único | `topology-init` único; serviços só verificam passivamente; módulo `platform` e ADR 0015 |
| 10 | Colisão de ids G × G | Requisitos renomeados para **RQ1 a RQ8**; gates seguem **G0 a G5** |
| 11 | Justificativa da D5 | Afirmação sobre `testcontainers-go` removida; justificativa passou a ser `SKIP LOCKED` no relay; decisão 3 registra o custo de escolher SQLite |

**Itens que continuam dependendo de você:** as seis decisões da seção 10 do plano (local e nome do projeto, `ms-notification`, banco, Loggly, onde mora o frontend, `ms-api` separado).
