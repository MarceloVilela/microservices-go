# Comparação v1 (PHP) × Go

Fonte e comandos: `specs/execucao-fase-9.log`. Medido em 2026-11-19, uma máquina (i5-8265U, Docker local), uma rodada. **Os domínios são diferentes** (v1: transações; Go: pipeline de fotos com download, miniatura, outbox, SSE e painel), então a comparação de tamanho e de recursos descreve o que cada um **faz**, não quem é melhor. A paridade pretendida é de comportamento de mensageria (`specs/oraculo-mensageria.md`).

| Medida | v1 (Lumen/PHP) | Go | Observação |
|---|---|---|---|
| Serviços de aplicação | 2 (`ms-transaction`, `ms-notification`) | 4 (`ms-ingest`, `ms-worker`, `ms-notification`, `ms-api`) + `topology-init` | o Go separa leitura/SSE e relay do outbox |
| Linhas de produção | 1.350 (PHP em `app/`, `routes/`, `database/`) | 5.433 (Go: 4 serviços, `ms-queues`, `platform/`, helpers de `tests/`; sem `ms-db`, `web` e scripts) | o Go inclui outbox, relay, SSRF, miniatura, SSE, métricas |
| Linhas de teste | 82 | 7.405 (`tests/` de integração 3.650; reprodutível com o método do log) | contagem de linhas, não de cobertura |
| Imagem da aplicação | 141 MB (`ms-transaction-app`; a do `ms-notification` do v1 não está listada: a tag local `ms-notification-app` hoje é a do Go) | 26,7 a 27 MB por serviço (`distroless/static`); `topology-init` 16,2 MB | só a aplicação; nginx e bancos fora dos dois lados |
| Memória ociosa | **não medido** | 5,1 a 6,7 MB por serviço (RabbitMQ 95 MB, Postgres 26 MB, Mailpit 16 MB, nginx do painel 7,7 MB) | `docker stats --no-stream`, stack parada de carga |
| Memória sob carga | **não medido** | pico do worker 32,7 MB (conc. 1) e 67,7 MB (conc. 4), `specs/execucao-fase-7-carga.log` | |
| Subida até `/readyz` | **não medido** | 0,4 a 0,8 s (`ms-api`, 3 rodadas, container já criado) | exclui build e pull |

## Por que o v1 não foi medido

O v1 é o oráculo e fica **intocado** (decisão do projeto): subir o v1 grava `.env`, `vendor/` e o banco dentro do repositório dele, e a máquina tem ~7 GB livres. Memória e subida do v1 ficam como lacuna declarada; rodar em um clone descartável do v1 fecha a lacuna se importar.

## O que a comparação **não** diz

- Não há comparação de vazão: o v1 não tem carga medida e o domínio é outro.
- Menos memória e imagem menor são propriedades esperadas de Go + `distroless` contra PHP-FPM; o valor do Go aqui está nas garantias de mensageria (outbox, idempotência, reconexão, shutdown gracioso), não no tamanho.
- Linhas por si só não medem esforço nem qualidade.
