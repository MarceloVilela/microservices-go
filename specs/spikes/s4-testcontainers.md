# S4: `testcontainers-go` (RabbitMQ 4 + PostgreSQL) e relay do outbox com `SKIP LOCKED`

Executado em 2026-11-17. Código em `specs/spikes/s4-testcontainers/` (`s4_test.go`, módulo isolado); saída bruta em `specs/spikes/s4-resultado.txt`.

**Ambiente:** Go 1.27.4, `testcontainers-go` v0.45.0 (módulos `postgres` e `rabbitmq`), `pgx/v5` v5.13.0, `amqp091-go` v1.17.0, `postgres:18-alpine` e `rabbitmq:4-management-alpine` (RabbitMQ 4.3.6), **ambas reaproveitadas das imagens locais**; Ryuk (`testcontainers/ryuk:0.14.0`, 4,8 MB) foi baixado na primeira execução. Máquina local, Docker local, sem segredo.

**Comando:** `go test -race -count=1 -v ./...` → **saída 0**, 2 testes `PASS`, ~34 s no total (conferido sem pipe, `echo $?` = `0`).

## Pergunta 1: `testcontainers-go` roda com RabbitMQ 4 e Postgres sem segredo?

**Sim.** Os módulos `postgres` e `rabbitmq` sobem os containers, esperam a prontidão (`Server startup complete`; `database system is ready` com a porta aberta) e limpam no fim (`CleanupContainer`, mais o Ryuk). Nenhuma credencial do projeto é necessária: usuário e senha do Postgres são descartáveis, o RabbitMQ usa o usuário padrão do módulo.

| Medida | Valor |
|---|---|
| Subida do Postgres | 5,5 a 13,9 s (varia; a 1ª execução incluiu o pull do Ryuk) |
| Subida do RabbitMQ | 6,3 a 8,0 s |
| Um teste completo (2 containers + teste + limpeza) | 15 a 24 s |

**Ressalva (revista):** o teste usa `User: "rabbitmq"` (via `testcontainers.CustomizeRequest`) porque, na hora, o `rabbitmq:4-management-alpine` local falhava como root com `eacces` (S1). Esse erro **não voltou a acontecer** mais tarde (ver o achado lateral corrigido no S1), então o `User` pode ser desnecessário; **não retestei o S4 sem ele**. **Não testei GitHub Actions**; o que foi provado é a execução local com o socket do Docker.

## Pergunta 2: o relay com `FOR UPDATE SKIP LOCKED` se comporta com duas instâncias?

Relay: `BEGIN; SELECT ... WHERE published_at IS NULL ORDER BY id LIMIT n FOR UPDATE SKIP LOCKED; publica com confirm; UPDATE published_at; COMMIT`.

| Teste | Resultado |
|---|---|
| **Duas instâncias** drenam 1.000 linhas (lote 20), conexões próprias a Postgres e RabbitMQ | A publicou 580 e B 420; **1.000 ids distintos, 1.000 mensagens, 0 duplicatas, 0 linhas sem `published_at`**; 131 a 166 ms |
| **A segura 50 linhas** num lote aberto (publicou e confirmou, ainda sem commit) | B publicou 150 de 200 e **nenhuma das 50 travadas** (`SKIP LOCKED` pulou as linhas de A) |
| **A "cai"** entre o confirm e a marca (rollback) | as 50 linhas voltam, B as publica de novo: **200 ids distintos de 200 (nenhuma perdida) e 250 mensagens (50 duplicatas)** |

Conclusão: com duas instâncias não há perda nem duplicata no caminho normal; uma queda entre o confirm e o commit **duplica** (publicação *at-least-once*), exatamente como prevê a ADR 0012, e por isso o worker precisa ser idempotente (ADR 0006).

## Limites desta evidência

- As duas "instâncias" são **goroutines do mesmo processo**, cada uma com a própria conexão AMQP; o pool do `pgx` é compartilhado, então as transações são independentes mas não são dois processos. Dois processos reais (ou containers) não foram testados.
- A "queda" foi simulada com `ROLLBACK`, não com `kill` do processo nem queda da conexão do Postgres.
- 1.000 linhas e lote de 20; uma execução (duas, contando a repetição sem pipe); sem medida de contenção sob carga maior.
- Não testei o relay com o broker fora do ar (isso é Fase 2).
- Não testei `ORDER BY id` com muitas instâncias (a ordem de publicação entre instâncias não é garantida; coerente com a ADR 0013).
- Não testei CI real (GitHub Actions); só execução local.
- Custo de disco medido: cache de módulos do Go ~308 MB e cache de build ~490 MB (mais que a estimativa de 0,3 a 1 GB somada ao S1 e S2).
