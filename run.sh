#!/bin/sh
# Sobe o RabbitMQ, declara a topologia (topology-init) e sobe os 4 serviços.
# Uso: ./run.sh                 sobe tudo
#      ./run.sh <servico> ...   docker compose do serviço (ex.: ./run.sh ms-ingest logs -f)
cd "$(dirname "$0")" || exit 1
set -eu

NETWORK=ms-go-network
SERVICES="ms-ingest ms-worker ms-notification ms-api"

# .env com senha aleatória no primeiro uso (não versionado)
if [ ! -f .env ]; then
  pass="$(od -An -tx1 -N16 /dev/urandom | tr -d ' \n')"
  printf 'RABBITMQ_USER=app\nRABBITMQ_PASS=%s\n' "$pass" > .env
  chmod 600 .env
fi

dc() { svc="$1"; shift; docker compose --env-file .env -f "$svc/docker-compose.yml" "$@"; }

if [ $# -gt 0 ]; then
  svc="$1"; shift
  dc "$svc" "$@"
  exit
fi

docker network inspect "$NETWORK" >/dev/null 2>&1 || docker network create "$NETWORK"

dc ms-queues up -d --wait ms-queues
dc ms-queues run --rm --build topology-init

for svc in $SERVICES; do
  dc "$svc" up -d --build
done
