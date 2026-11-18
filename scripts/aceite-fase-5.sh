#!/usr/bin/env bash
# Aceite da Fase 5 (ms-notification), com o broker, o Mailpit e o Lorem Picsum de verdade.
# Requer a pilha de pé (./run.sh) e internet. Para e religa o ms-notification (SIGTERM).
# Sai com 0 só se todos os critérios passarem. Uso: scripts/aceite-fase-5.sh
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck disable=SC1091
. ./.env
MGMT="http://localhost:15672/api"
AUTH="$RABBITMQ_USER:$RABBITMQ_PASS"
INGEST="http://localhost:8081"
WORKER="http://localhost:8083"
NOTIF="http://localhost:8084"
MAILPIT="http://localhost:8025/api/v1"
RUN="$(date +%s)-$$"
fails=0

ok()   { printf 'ok    %s\n' "$1"; }
fail() { printf 'FALHA %s\n' "$1"; fails=$((fails + 1)); }
expect() { if [ "$2" = "$3" ]; then ok "$1 ($3)"; else fail "$1: esperado $2, obtido $3"; fi; }
sql() { docker exec ms-db psql -U app -d app -tAqc "$1"; }
wait_for() { # esperado descrição comando...  (até 120 s)
  local want="$1" what="$2" got=""; shift 2
  for _ in $(seq 120); do
    got="$("$@" 2>/dev/null || true)"; [ "$got" = "$want" ] && break; sleep 1
  done
  expect "$what" "$want" "$got"
}
qcount() { curl -s -u "$AUTH" "$MGMT/queues/%2F/$1" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("messages",0))'; }
status() { curl -s -o /dev/null -w '%{http_code}' "$@" || true; }
connected() { curl -s "$NOTIF/statusz" | python3 -c 'import sys,json; print(str(json.load(sys.stdin)["consumer_connected"]).lower())'; }
post() { curl -s -w ' %{http_code}' -X POST "$INGEST/batches" -H 'Content-Type: application/json' -H "Idempotency-Key: $1" -d "$2" --max-time 5 || true; }
batch_id() { printf '%s' "${1% *}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["batch_id"])'; }
mails() { curl -s -G "$MAILPIT/search" --data-urlencode "query=$1" | python3 -c 'import sys,json; print(json.load(sys.stdin)["messages_count"])'; }
notified() { sql "SELECT count(*) FROM notifications WHERE batch_id='$1'"; }
log_lines() { docker logs ms-notification 2>&1 | grep -c "lote concluído.*$1" || true; }

echo "== 1. pilha pronta"
docker start ms-worker ms-notification >/dev/null 2>&1 || true
wait_for 200 "ms-worker /readyz" status "$WORKER/readyz"
wait_for 200 "ms-ingest /readyz" status "$INGEST/readyz"
wait_for 200 "ms-notification /readyz" status "$NOTIF/readyz"
wait_for 200 "Mailpit" status "$MAILPIT/info"
wait_for true "consumidor do ms-notification conectado" connected

echo "== 2. um lote termina e o e-mail chega UMA vez (uma foto válida, uma inválida)"
out="$(post "aceite5-$RUN" '{"photos":[{"source_url":"https://picsum.photos/id/21/1200/800"},{"source_url":"https://picsum.photos/id/9999/1200/800"}]}')"
expect "POST /batches" 202 "${out##* }"
id="$(batch_id "$out")"
wait_for 1 "e-mail no Mailpit" mails "$id"
wait_for 1 "linha em notifications" notified "$id"
expect "log de notificação (slog)" 1 "$(log_lines "$id")"
assunto="$(curl -s -G "$MAILPIT/search" --data-urlencode "query=$id" | python3 -c 'import sys,json; print(json.load(sys.stdin)["messages"][0]["Subject"])')"
case "$assunto" in *"$id"*) ok "assunto menciona o lote" ;; *) fail "assunto sem o lote: $assunto" ;; esac

echo "== 3. o MESMO batch.completed reentregue não gera segundo e-mail nem segundo log"
payload="$(sql "SELECT payload::text FROM outbox WHERE routing_key='batch.completed' AND payload->>'batch_id'='$id'")"
body="$(python3 - "$payload" <<'PY'
import json, sys
print(json.dumps({"properties": {"delivery_mode": 2, "message_id": json.loads(sys.argv[1])["message_id"]},
                  "routing_key": "batch.completed", "payload": sys.argv[1], "payload_encoding": "string"}))
PY
)"
routed="$(curl -s -u "$AUTH" -X POST "$MGMT/exchanges/%2F/photos/publish" -H 'Content-Type: application/json' -d "$body" | python3 -c 'import sys,json; print(str(json.load(sys.stdin)["routed"]).lower())')"
expect "mensagem republicada e roteada" true "$routed"
wait_for 0 "batch.completed consumida" qcount batch.completed
sleep 3
expect "e-mails do lote depois da reentrega" 1 "$(mails "$id")"
expect "logs do lote depois da reentrega" 1 "$(log_lines "$id")"
expect "linhas em notifications" 1 "$(notified "$id")"

echo "== 4. SIGTERM: o ms-notification encerra com saída 0"
docker stop -t 10 ms-notification >/dev/null
expect "saída do ms-notification" 0 "$(docker inspect -f '{{.State.ExitCode}}' ms-notification)"
docker start ms-notification >/dev/null
wait_for 200 "ms-notification /readyz" status "$NOTIF/readyz"

echo
if [ "$fails" -eq 0 ]; then echo "ACEITE DA FASE 5: OK"; else echo "ACEITE DA FASE 5: $fails FALHA(S)"; exit 1; fi
