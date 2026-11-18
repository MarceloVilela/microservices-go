# specs/

Entregáveis das fases do plano (`docs/plan-go.md`). As ADRs ficam em `docs/adr/` (o plano dizia `specs/adr/`; decisão F0-5).

| Arquivo | O que é | Fase |
|---|---|---|
| `fase-0-auditoria-spec-spikes.md` | spec da Fase 0: decisões, entregáveis, estado e aceite | 0 |
| `auditoria-v1.md` | achados da leitura e da execução do v1 | 0 |
| `oraculo-mensageria.md` | cenários O1 a O5 (v1 executado; Go nas Fases 3 e 4) | 0 |
| `oraculo-v1-resultado.txt` | saída bruta do oráculo contra o v1 | 0 |
| `dataset-de-paridade.md` | lote `paridade-1`, casos 1 a 10 e regras assumidas | 0 |
| `dataset-server/` | servidor local de teste dos casos 9 e 10 e do `Retry-After` | 0 |
| `spikes/` | S1 a S5: documento, código e saída bruta de cada um | 0 |
| `execucao-fase-0.log` | registro de execução e correções da fase | 0 |
| `fase-1-esqueleto.md` | spec da Fase 1: decisões F1-1 a F1-9, topologia, variáveis, aceite, limites | 1 |
| `execucao-fase-1.log` | evidência: `make check`, integração, aceite e container limpo | 1 |

Padrões futuros (ainda não existem): `events.schema.json` e `acceptance/*.http`; `fase-N-nome.md` e `execucao-fase-N.log` seguem o mesmo padrão nas próximas fases.
