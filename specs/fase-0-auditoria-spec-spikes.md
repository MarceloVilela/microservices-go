# Fase 0: auditoria do v1, spec, ADRs e spikes

Branch: `fase-0-auditoria-spec-spikes`. Plano: `docs/plan-go.md` (seção 7).

## Decisões da fase

| # | Decisão | Data |
|---|---|---|
| F0-1 | `ms-notification` é serviço próprio (Fase 5) | 2026-11-17 |
| F0-2 | Painel Angular em **repositório separado**; G5 cruza dois repositórios (ADR 0019) | 2026-11-17 |
| F0-3 | `ms-api` separado do `ms-ingest` | 2026-11-17 |
| F0-4 | Loggly **descartado**; notificação por interface `Notifier` (`slog` padrão, SMTP/Mailpit opcional), ADR 0020 | 2026-11-17 |
| F0-5 | ADRs ficam em `docs/adr/` (o plano dizia `specs/adr/`) | 2026-11-17 |

## Entregáveis e estado

| Entregável | Arquivo | Estado |
|---|---|---|
| Auditoria do v1 | `specs/auditoria-v1.md` | escrita (16 achados, por leitura do código) |
| Oráculo de mensageria | `specs/oraculo-mensageria.md` | roteiros escritos; **lado v1 executado** (O1 a O5 batem com o esperado); lado Go só nas Fases 3 e 4 |
| Dataset de paridade | `specs/dataset-de-paridade.md` + `specs/dataset-server/` | escrito: casos 1 a 10 definidos; 9 e 10 e o `Retry-After` têm servidor local testado; miniatura (200 px) e teto (25 Mpx) são **propostas** a aprovar |
| ADRs 0001 a 0020 | `docs/adr/` | escritas como `proposta`; nenhuma aceita |
| S3 (Lorem Picsum) | `specs/spikes/s3-picsum.md` | **executado**, atende |
| S1 (confirms e reconexão) | `specs/spikes/s1-amqp.md` | **executado** (fila clássica, uma execução) |
| S2 (retry TTL + DLX, quorum) | `specs/spikes/s2-retry.md` | **executado**; decisão do usuário: **escada TTL + DLX** (ADR 0005 aceita) |
| S4 (`testcontainers-go`, relay com `SKIP LOCKED`) | `specs/spikes/s4-testcontainers.md` | **executado** (instâncias como goroutines; só local, sem CI real) |
| S5 (imagem final: HTTPS e healthcheck) | `specs/spikes/s5-imagem.md` | **executado** (binário compilado no host; multi-stage não testado) |
| Referências cruzadas conferidas | ver `specs/execucao-fase-0.log` | **feita** (2026-11-17); renomeações `zgo.md`→`go101.md` e `zangular.md`→`angular101.md` corrigidas; `specs/README.md` criado |

## Aceite (do plano) e situação

Auditoria, oráculo e dataset escritos: **sim** (oráculo: lado v1 executado; lado Go só nas Fases 3 e 4). ADRs 0001
a 0020 escritas: **sim**; "as com evidência, aceitas": **sim**, 8 aceitas em 2026-11-18 (0001, 0003, 0004, 0005, 0007,
0010, 0011, 0012); as outras 12 ficam `proposta` até as fases correspondentes, por decisão do usuário (tabela em
`docs/adr/README.md`). S1 a S5 com número: **sim**.

**G0 fechado em 2026-11-18** (critério do plano: S1 a S4 com resultado medido; o S5 também foi feito). Pendências que
**não** bloqueiam o G0 e seguem para as fases: oráculo do lado Go (Fases 3 e 4), `sqlc` (Fase 2), rate limit do Picsum e
teste de entrada hostil (Fase 7), multi-stage real e versão do Go (Fase 1), mecanismo de publicação do contrato entre
repositórios (ADR 0019).

**Próximo passo:** PR da Fase 0 (**só quando o usuário pedir**; plano: um PR por fase, merge antes da Fase 1).
