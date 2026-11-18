#!/usr/bin/env bash
# Inspeciona a DLQ (photo.process.dlq) SEM consumir: cada mensagem é lida e devolvida
# (ackmode=ack_requeue_true). Mostra message_id, tentativa, classe e motivo da falha.
# Requer a pilha de pé (./run.sh). Uso: scripts/dlq.sh [limite]   (padrão 20)
set -euo pipefail
cd "$(dirname "$0")/.."
# shellcheck disable=SC1091
. ./.env
LIMIT="${1:-20}"
curl -fsS -u "$RABBITMQ_USER:$RABBITMQ_PASS" -H 'Content-Type: application/json' \
  -d "{\"count\":$LIMIT,\"ackmode\":\"ack_requeue_true\",\"encoding\":\"auto\"}" \
  "http://localhost:15672/api/queues/%2F/photo.process.dlq/get" | python3 -c '
import sys, json
msgs = json.load(sys.stdin)
print("%d mensagem(ns) lida(s) (devolvidas à fila)" % len(msgs))
for m in msgs:
    props = m["properties"]
    h = props.get("headers", {})
    body = json.loads(m["payload"]) if m["payload"].startswith("{") else {}
    print("- photo_id=%s message_id=%s x-attempt=%s x-failure-class=%s x-failure-reason=%r" % (
        body.get("photo_id", "?"), props.get("message_id", "?"), h.get("x-attempt", "?"),
        h.get("x-failure-class", "?"), h.get("x-failure-reason", "?")))
'
