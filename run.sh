#!/bin/sh
# Sobe o Postgres e o RabbitMQ, declara a topologia (topology-init) e sobe os 4 serviços.
# Uso: ./run.sh                 sobe tudo
#      ./run.sh <servico> ...   docker compose do serviço (ex.: ./run.sh ms-ingest logs -f)
cd "$(dirname "$0")" || exit 1
set -eu

NETWORK=ms-go-network
SERVICES="ms-ingest ms-worker ms-notification ms-api"

# .env com senhas aleatórias no primeiro uso (não versionado); completa o que faltar
rand() { od -An -tx1 -N16 /dev/urandom | tr -d ' \n'; }
[ -f .env ] || { : > .env; chmod 600 .env; }
# garante a quebra de linha final, para o que for acrescentado não colar na última linha
[ ! -s .env ] || [ -z "$(tail -c1 .env)" ] || echo >> .env
grep -q '^RABBITMQ_USER=' .env || echo 'RABBITMQ_USER=app' >> .env
grep -q '^RABBITMQ_PASS=' .env || echo "RABBITMQ_PASS=$(rand)" >> .env
grep -q '^POSTGRES_PASS=' .env || echo "POSTGRES_PASS=$(rand)" >> .env

dc() { svc="$1"; shift; docker compose --env-file .env -f "$svc/docker-compose.yml" "$@"; }

if [ $# -gt 0 ]; then
  svc="$1"; shift
  dc "$svc" "$@"
  exit
fi

docker network inspect "$NETWORK" >/dev/null 2>&1 || docker network create "$NETWORK"

dc ms-db up -d --wait ms-db
dc ms-queues up -d --wait ms-queues
dc ms-queues run --rm --build topology-init

for svc in $SERVICES; do
  dc "$svc" up -d --build
done
