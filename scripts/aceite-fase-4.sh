#!/usr/bin/env bash
# Aceite da Fase 4 (resiliência), com o broker real do compose e o Lorem Picsum de verdade.
# Requer a pilha de pé (./run.sh) e internet. REINICIA o ms-queues (broker) com o worker no ar.
# Sai com 0 só se todos os critérios passarem. Uso: scripts/aceite-fase-4.sh
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck disable=SC1091
. ./.env
MGMT="http://localhost:15672/api"
AUTH="$RABBITMQ_USER:$RABBITMQ_PASS"
INGEST="http://localhost:8081"
WORKER="http://localhost:8083"
RUN="$(date +%s)-$$"
fails=0

ok()   { printf 'ok    %s\n' "$1"; }
fail() { printf 'FALHA %s\n' "$1"; fails=$((fails + 1)); }
expect() { if [ "$2" = "$3" ]; then ok "$1 ($3)"; else fail "$1: esperado $2, obtido $3"; fi; }
sql() { docker exec ms-db psql -U app -d app -tAqc "$1"; }
wait_for() { # esperado descrição comando...  (até 180 s)
  local want="$1" what="$2" got=""; shift 2
  for _ in $(seq 180); do
    got="$("$@" 2>/dev/null || true)"; [ "$got" = "$want" ] && break; sleep 1
  done
  expect "$what" "$want" "$got"
}
qcount() { curl -s -u "$AUTH" "$MGMT/queues/%2F/$1" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("messages_ready",0))'; }
status() { curl -s -o /dev/null -w '%{http_code}' "$@" || true; }
connected() { curl -s "$WORKER/statusz" | python3 -c 'import sys,json; print(str(json.load(sys.stdin)["consumer_connected"]).lower())'; }
post() { curl -s -w ' %{http_code}' -X POST "$INGEST/batches" -H 'Content-Type: application/json' -H "Idempotency-Key: $1" -d "$2" --max-time 5 || true; }
batch_id() { printf '%s' "${1% *}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["batch_id"])'; }
completed() { sql "SELECT count(*) FROM batches WHERE id='$1' AND completed_at IS NOT NULL"; }
restart_broker() {
  docker restart ms-queues >/dev/null
  for _ in $(seq 60); do [ "$(status -u "$AUTH" "$MGMT/overview")" = 200 ] && return 0; sleep 1; done
  return 1
}

echo "== 1. pilha pronta"
docker start ms-worker >/dev/null 2>&1 || true
wait_for 200 "ms-worker /readyz" status "$WORKER/readyz"
wait_for 200 "ms-ingest /readyz" status "$INGEST/readyz"
wait_for true "consumidor conectado" connected
wait_for 0 "photo.process vazia" qcount photo.process

echo "== 2. O4 (R5b): mensagens enfileiradas com o worker parado sobrevivem ao reinício do broker"
docker stop ms-worker >/dev/null
out="$(post "aceite4-$RUN-a" '{"photos":[{"source_url":"https://picsum.photos/id/11/1200/800"},{"source_url":"https://picsum.photos/id/12/1200/800"},{"source_url":"https://picsum.photos/id/13/1200/800"}]}')"
expect "POST /batches" 202 "${out##* }"
id="$(batch_id "$out")"
wait_for 3 "photo.process com 3 Ready" qcount photo.process
restart_broker || fail "o broker não voltou"
wait_for 3 "photo.process ainda com 3 Ready depois do reinício" qcount photo.process
docker start ms-worker >/dev/null
wait_for 1 "lote terminado" completed "$id"
expect "fotos done" 3 "$(sql "SELECT count(*) FROM photos WHERE batch_id='$id' AND status='done'")"

echo "== 3. broker reiniciado com o worker no ar: o MESMO processo volta a consumir"
wait_for true "consumidor conectado" connected
pid_antes="$(docker inspect -f '{{.State.StartedAt}}' ms-worker)"
restart_broker || fail "o broker não voltou"
wait_for true "consumidor reconectou sozinho" connected
expect "worker não foi reiniciado" "$pid_antes" "$(docker inspect -f '{{.State.StartedAt}}' ms-worker)"
out2="$(post "aceite4-$RUN-b" '{"photos":[{"source_url":"https://picsum.photos/id/14/1200/800"},{"source_url":"https://picsum.photos/id/15/1200/800"}]}')"
expect "POST depois do reinício" 202 "${out2##* }"
id2="$(batch_id "$out2")"
wait_for 1 "lote 2 terminado sem reiniciar o worker" completed "$id2"

echo "== 4. id inválido do dataset termina na DLQ com a causa, e a DLQ é inspecionável sem consumir"
sleep 6   # as estatísticas do painel se atualizam a cada ~5 s; sem isto dlq0 pode vir velho
dlq0="$(qcount photo.process.dlq)"
out3="$(post "aceite4-$RUN-c" '{"photos":[{"source_url":"https://picsum.photos/id/9999/1200/800"}]}')"
id3="$(batch_id "$out3")"
wait_for 1 "lote 3 terminado" completed "$id3"
expect "foto failed" 1 "$(sql "SELECT count(*) FROM photos WHERE batch_id='$id3' AND status='failed'")"
wait_for "$((dlq0 + 1))" "mensagem na DLQ" qcount photo.process.dlq
listagem="$(scripts/dlq.sh 100)"
case "$listagem" in *"x-failure-class='permanent'"*|*"x-failure-class=permanent"*) ok "DLQ mostra a classe" ;; *) fail "scripts/dlq.sh não mostra x-failure-class" ;; esac
case "$listagem" in *"HTTP 404"*) ok "DLQ mostra o motivo (HTTP 404)" ;; *) fail "scripts/dlq.sh não mostra o motivo" ;; esac
expect "dlq.sh não consome" "$((dlq0 + 1))" "$(qcount photo.process.dlq)"

echo "== 5. SIGTERM: o worker encerra com saída 0"
docker stop -t 10 ms-worker >/dev/null
expect "saída do ms-worker" 0 "$(docker inspect -f '{{.State.ExitCode}}' ms-worker)"
docker start ms-worker >/dev/null
wait_for 200 "ms-worker /readyz" status "$WORKER/readyz"

echo
if [ "$fails" -eq 0 ]; then echo "ACEITE DA FASE 4: OK"; else echo "ACEITE DA FASE 4: $fails FALHA(S)"; exit 1; fi
