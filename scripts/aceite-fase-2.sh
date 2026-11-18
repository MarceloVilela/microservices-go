#!/usr/bin/env bash
# Aceite da Fase 2 (ms-ingest, Postgres e outbox), ponta a ponta. Requer a pilha de pé (./run.sh).
# Derruba e religa o ms-queues (RabbitMQ). Sai com 0 só se todos os critérios passarem.
# Uso: scripts/aceite-fase-2.sh
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck disable=SC1091
. ./.env
MGMT="http://localhost:15672/api"
AUTH="$RABBITMQ_USER:$RABBITMQ_PASS"
INGEST="http://localhost:8081"
RUN="$(date +%s)-$$"
fails=0

ok()   { printf 'ok    %s\n' "$1"; }
fail() { printf 'FALHA %s\n' "$1"; fails=$((fails + 1)); }
expect() { if [ "$2" = "$3" ]; then ok "$1 ($3)"; else fail "$1: esperado $2, obtido $3"; fi; }
sql() { docker exec ms-db psql -U app -d app -tAqc "$1"; }
# até 90 s esperando que o comando "$@" imprima o valor esperado ($1)
wait_for() { # esperado descrição comando...
  local want="$1" what="$2" got=""; shift 2
  for _ in $(seq 90); do
    got="$("$@" 2>/dev/null || true)"; [ "$got" = "$want" ] && break; sleep 1
  done
  expect "$what" "$want" "$got"
}
ready_msgs() { curl -s -u "$AUTH" "$MGMT/queues/%2F/photo.process" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("messages_ready",0))'; }
pending() { sql "SELECT count(*) FROM outbox WHERE published_at IS NULL"; }
status() { curl -s -o /dev/null -w '%{http_code}' "$@" || true; }
body() { # n fotos
  python3 -c 'import json,sys; n=int(sys.argv[1]); print(json.dumps({"photos":[{"source_url":"https://picsum.photos/id/%d/1200/800"%(i+10)} for i in range(n)]}))' "$1"
}
post_status() { # chave json -> imprime só o código HTTP
  curl -s -o /dev/null -w '%{http_code}' -X POST "$INGEST/batches" -H 'Content-Type: application/json' -H "Idempotency-Key: $1" -d "$2" --max-time 5 || true
}
post() { # chave json -> imprime "corpo status" (o status é a última palavra)
  curl -s -w ' %{http_code}' -X POST "$INGEST/batches" -H 'Content-Type: application/json' -H "Idempotency-Key: $1" -d "$2" --max-time 5 || true
}

echo "== 1. lote válido: 202, fotos e outbox na mesma transação, fila com N mensagens"
wait_for 200 "ms-ingest /readyz" status "$INGEST/readyz"
base="$(ready_msgs)"
out="$(post "aceite-$RUN-a" "$(body 3)")"
expect "POST /batches" 202 "${out##* }"
id="$(printf '%s' "${out% *}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["batch_id"])')"
expect "fotos do lote" 3 "$(sql "SELECT count(*) FROM photos WHERE batch_id='$id'")"
expect "evento batch.created" 1 "$(sql "SELECT count(*) FROM events WHERE batch_id='$id' AND type='batch.created'")"
wait_for 0 "outbox sem pendentes" pending
wait_for "$((base + 3))" "fila photo.process (Ready) cresceu 3" ready_msgs

echo "== 2. Idempotency-Key repetida devolve o mesmo lote"
out2="$(post "aceite-$RUN-a" "$(body 3)")"
expect "repetição" 200 "${out2##* }"
expect "mesmo batch_id" "$id" "$(printf '%s' "${out2% *}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["batch_id"])')"
expect "mesma chave, outro corpo" 422 "$(post_status "aceite-$RUN-a" "$(body 4)")"
expect "lotes com essa chave" 1 "$(sql "SELECT count(*) FROM batches WHERE idempotency_key='aceite-$RUN-a'")"

echo "== 3. corpo inválido dá 400 e não grava nada"
before="$(sql "SELECT count(*) FROM batches")"
expect "host fora da allowlist" 400 "$(post_status "aceite-$RUN-b" '{"photos":[{"source_url":"https://example.com/foto.jpg"}]}')"
expect "lista vazia" 400 "$(post_status "aceite-$RUN-c" '{"photos":[]}')"
expect "JSON quebrado" 400 "$(post_status "aceite-$RUN-d" '{"photos":')"
expect "lotes depois dos 400" "$before" "$(sql "SELECT count(*) FROM batches")"

echo "== 4. broker parado: POST ainda dá 202, outbox acumula, /readyz falha; ao voltar, publica tudo"
base="$(ready_msgs)"
docker stop ms-queues >/dev/null
wait_for 503 "ms-ingest /readyz com broker parado" status "$INGEST/readyz"
out3="$(post "aceite-$RUN-e" "$(body 5)")"
expect "POST com broker parado" 202 "${out3##* }"
sleep 2
expect "pendentes no outbox" 5 "$(pending)"
docker start ms-queues >/dev/null
# 1ª partida do RabbitMQ pode cair com eacces (specs/spikes/s1-amqp.md); o compose reinicia
wait_for 0 "outbox sem pendentes depois que o broker voltou" pending
wait_for "$((base + 5))" "fila photo.process recebeu as 5 do lote" ready_msgs
wait_for 200 "ms-ingest /readyz depois que o broker voltou" status "$INGEST/readyz"

echo "== 5. config check do ms-ingest (DATABASE_URL obrigatória; nenhum valor é impresso)"
set +e
docker run --rm -e AMQP_URL=amqp://u:p@h/ ms-ingest-app config check >/dev/null 2>&1; expect "sem DATABASE_URL sai 1" 1 "$?"
out="$(docker run --rm -e AMQP_URL=amqp://u:senha-secreta@h/ -e DATABASE_URL=postgres://u:senha-do-banco@h/d ms-ingest-app config check 2>&1)"; code=$?
expect "com AMQP_URL e DATABASE_URL sai 0" 0 "$code"
case "$out" in *senha-secreta*|*senha-do-banco*) fail "config check imprimiu um segredo" ;; *) ok "config check não imprime segredos" ;; esac
set -e

echo
if [ "$fails" -eq 0 ]; then echo "ACEITE DA FASE 2: OK"; else echo "ACEITE DA FASE 2: $fails FALHA(S)"; exit 1; fi
