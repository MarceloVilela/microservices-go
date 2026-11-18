#!/bin/sh
# Para os serviços e o RabbitMQ. ./stop.sh -v apaga também os volumes.
cd "$(dirname "$0")" || exit 1
# o compose interpola as variáveis mesmo no "down"; valores de mentira bastam
export RABBITMQ_USER="${RABBITMQ_USER:-x}" RABBITMQ_PASS="${RABBITMQ_PASS:-x}"
for svc in ms-api ms-notification ms-worker ms-ingest ms-queues; do
  docker compose -f "$svc/docker-compose.yml" down "$@"
done
