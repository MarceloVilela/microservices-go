# Fase 9: fechamento

Branch: `fase-9-fechamento` (a partir da `main` com as Fases 0 a 8, PR #9 mergeado). Plano: `docs/plan-go.md` (seção 7, Fase 9).
Status: **spec e execução em 2026-11-19**; sem código novo de serviço (só documentação e medição). Code-review `high`: 8 achados, 1 falso (CLAUDE.local.md já estava atualizado), 7 corrigidos.

## Escopo (do plano)

Checklist de long tail, README honesto, comparação PHP v1 × Go, limitações e `CHANGELOG.md`.

## Entregáveis

| # | Entrega | Onde |
|---|---|---|
| F9-1 | Checklist de long tail (skill `fechamento-versao`) rodado contra o repositório real | seção "Checklist (resultado)" abaixo; medições em `specs/execucao-fase-9.log` |
| F9-2 | Comparação v1 × Go (imagem, memória, linhas, tempo de subida) | `docs/comparacao-v1-go.md` |
| F9-3 | README reescrito: status atual, domínio diferente, o que é opcional, limitações | `README.md` |
| F9-4 | `CHANGELOG.md` (uma entrada por fase) | raiz |
| F9-5 | Docs sincronizados (`plan-go.md`, `CLAUDE.md`, índice de ADRs) | vários |

## Método da comparação (sem meta inventada)

- **Linhas**: `wc -l` sobre `*.go` (produção × `_test.go`) e sobre `*.php` de `app/`, `routes/`, `database/` (e `tests/`) do v1. Domínios diferentes: o número descreve tamanho, não qualidade.
- **Imagem**: `docker images` do que cada lado constrói (v1: `ms-transaction-app`; Go: imagens finais `distroless`). Só a imagem da aplicação; nginx/MySQL/Postgres/RabbitMQ ficam fora dos dois lados.
- **Memória**: `docker stats --no-stream` dos serviços Go **ociosos** (stack de pé). **v1 não foi medido**: o v1 é oráculo intocado (rodá-lo grava `.env`, `vendor` e banco nele) e há ~7 GB de disco livres.
- **Subida**: `docker stop` + `docker start` + sondagem do `/readyz` do `ms-api` (3 rodadas). v1 não medido, pelo mesmo motivo.

O que não foi medido entra como **não medido** na tabela, não como estimativa.

## Aceite

- `docs/comparacao-v1-go.md` e `CHANGELOG.md` existem; o README não afirma nada sem fonte versionada.
- Referências cruzadas conferidas (arquivos citados existem).
- `make check` com saída `0` (nada de código mudou; confirma que a doc não quebrou nada, ex.: teste que lê arquivos).

## Fora do escopo (decisão do usuário necessária, não feita)

- `git tag contract-v1` e push da tag (ação externa; o CI do painel depende dela).
- `git init` do painel, segredo `BACKEND_READ_TOKEN`.
- Aprovação das ADRs `proposta` e decisão da ADR 0022.

## Checklist (resultado, 2026-11-19)

**Conferido**
- Referências cruzadas: todos os caminhos citados em README, CHANGELOG, CLAUDE.md e docs novos existem (docs do painel em `../microservices-frontend-angular/docs/`).
- ADRs: 22 (12 aceitas, 10 `proposta`) = índice em `docs/adr/README.md`, README e CLAUDE.md.
- Tabela de fases do `plan-go.md` e blocos de status das Fases 4 a 8 sincronizados com os PRs #5 a #9.
- `make check` com saída `0` (build, vet, `-race`, arch, lint, vuln).

**Estava desatualizado (corrigido)**: status "sem PR" das Fases 4 a 6 e "Nada foi implementado" no `plan-go.md`; seção 10 tratava `sqlc` como pendente; contagem de linhas do Go (5.390 → 5.433); CHANGELOG sem marcar a Fase 9 como em andamento.

**Fica para depois (rastreado)**: ADR 0022 (foto parada na DLQ). Fechá-la agora **muda o contrato** (G5: código 409, evento SSE de foto parada), com impacto no painel já mergeado; decidir com o usuário e versionar `contract-v2`. Tag `contract-v1`, CI do painel e aprovação das 10 ADRs `proposta` dependem do usuário. Memória e subida do v1 não medidas.
