# ms-db

O Postgres 18 (`postgres:18-alpine`) do projeto. Só o `docker-compose.yml`: não há código Go aqui.

## Dados

| Item | Valor |
|---|---|
| Porta | `127.0.0.1:5432` |
| Banco e usuário | `app` |
| Senha | `POSTGRES_PASS` do `.env`, gerada pelo `run.sh` (não versionada) |
| Volume | `ms-db-data` (`./stop.sh -v` apaga) |

## Esquema

As migrações ficam em `platform/db/migrations` (`0001_init`, `0002_worker`, `0003_notification`, `0004_api`). **Só o `ms-ingest` migra**; os demais serviços verificam o esquema no boot e falham o `/readyz` se faltar algo. Acesso a dados por `pgx` direto, sem `sqlc` (ADR 0007).

Tabelas principais: lotes, fotos, outbox (com quarentena), `events` (log com `seq` do SSE) e `notifications` (dedup do `ms-notification`).

## Rodar

`./run.sh` (raiz) sobe o banco e espera ficar saudável (`pg_isready`) antes dos serviços. Acesso manual: `docker exec -it ms-db psql -U app app`.
