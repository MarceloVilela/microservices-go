#!/usr/bin/env bash
# Aceite da Fase 6 (ms-api), com o broker, o Postgres e o Lorem Picsum de verdade.
# Requer a pilha de pé (./run.sh) e internet. Para e religa o ms-api (docker stop/start).
# Sai com 0 só se todos os critérios passarem. Uso: scripts/aceite-fase-6.sh
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck disable=SC1091
. ./.env
INGEST="http://localhost:8081"
WORKER="http://localhost:8083"
API="http://localhost:8082"
TOKEN="$API_DEMO_TOKEN"
RUN="$(date +%s)-$$"
TMP="$(mktemp -d)"
fails=0
curl_pid=""
cleanup() { [ -z "$curl_pid" ] || kill "$curl_pid" 2>/dev/null || true; rm -rf "$TMP"; docker start ms-api >/dev/null 2>&1 || true; }
trap cleanup EXIT

ok()   { printf 'ok    %s\n' "$1"; }
fail() { printf 'FALHA %s\n' "$1"; fails=$((fails + 1)); }
expect() { if [ "$2" = "$3" ]; then ok "$1 ($3)"; else fail "$1: esperado $2, obtido $3"; fi; }
sql() { docker exec ms-db psql -U app -d app -tAqc "$1"; }
status() { curl -s -o /dev/null -w '%{http_code}' "$@" || true; }
api() { curl -s -H "Authorization: Bearer $TOKEN" "$@"; }
wait_for() { # esperado descrição comando...  (até 120 s)
  local want="$1" what="$2" got=""; shift 2
  for _ in $(seq 120); do
    got="$("$@" 2>/dev/null || true)"; [ "$got" = "$want" ] && break; sleep 1
  done
  expect "$what" "$want" "$got"
}
post() { curl -s -w ' %{http_code}' -X POST "$INGEST/batches" -H 'Content-Type: application/json' -H "Idempotency-Key: $1" -d "$2" --max-time 5 || true; }
batch_id() { printf '%s' "${1% *}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["batch_id"])'; }
jget() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"; }
completed() { sql "SELECT count(*) FROM batches WHERE id='$1' AND completed_at IS NOT NULL"; }
photo_status() { sql "SELECT status FROM photos WHERE id='$1'"; }
last_id() { grep '^id: ' "$1" | tail -1 | cut -d' ' -f2; }

echo "== 1. pilha pronta"
docker start ms-worker ms-api >/dev/null 2>&1 || true
wait_for 200 "ms-api /readyz" status "$API/readyz"
wait_for 200 "ms-worker /readyz" status "$WORKER/readyz"
wait_for 200 "ms-ingest /readyz" status "$INGEST/readyz"

echo "== 2. sem token ou de origem estranha: recusado (SSE e miniatura incluídos)"
for r in /batches /photos /dlq /events /photos/00000000-0000-4000-8000-000000000000/thumb; do
  expect "GET $r sem token" 401 "$(status "$API$r")"
done
expect "POST /dlq/{id}/retry sem token" 401 "$(status -X POST "$API/dlq/00000000-0000-4000-8000-000000000000/retry")"
expect "token na query não vale" 401 "$(status "$API/events?token=$TOKEN")"
expect "origem fora da allowlist (com token)" 403 "$(status -H "Authorization: Bearer $TOKEN" -H 'Origin: http://evil.example' "$API/batches")"
expect "origem fora da allowlist no SSE" 403 "$(status -H "Authorization: Bearer $TOKEN" -H 'Origin: http://evil.example' "$API/events")"
expect "com token" 200 "$(status -H "Authorization: Bearer $TOKEN" "$API/batches")"

echo "== 3. SSE: pending → done|failed ao vivo, e heartbeat em silêncio"
curl -sN -H "Authorization: Bearer $TOKEN" "$API/events" > "$TMP/stream1" &
curl_pid=$!
sleep 1
out="$(post "aceite6-$RUN" '{"photos":[{"source_url":"https://picsum.photos/id/21/1200/800"},{"source_url":"https://picsum.photos/id/9999/1200/800"}]}')"
expect "POST /batches" 202 "${out##* }"
b1="$(batch_id "$out")"
wait_for 1 "lote 1 concluído" completed "$b1"
sleep 2
expect "evento batch.created" 1 "$(grep -c '^event: batch.created' "$TMP/stream1")"
expect "evento photo.done" 1 "$(grep -c '^event: photo.done' "$TMP/stream1")"
expect "evento photo.failed" 1 "$(grep -c '^event: photo.failed' "$TMP/stream1")"
expect "evento batch.completed" 1 "$(grep -c '^event: batch.completed' "$TMP/stream1")"
sleep 16
if grep -q '^: ping' "$TMP/stream1"; then ok "heartbeat em silêncio"; else fail "sem ': ping' depois de 16 s de silêncio"; fi
L="$(last_id "$TMP/stream1")"
kill "$curl_pid" 2>/dev/null || true; curl_pid=""

echo "== 4. Last-Event-ID depois de reiniciar o ms-api: sem perda e sem repetição"
docker stop -t 15 ms-api >/dev/null
expect "saída do ms-api" 0 "$(docker inspect -f '{{.State.ExitCode}}' ms-api)"
out="$(post "aceite6b-$RUN" '{"photos":[{"source_url":"https://picsum.photos/id/22/1200/800"}]}')"
expect "POST /batches com o ms-api parado" 202 "${out##* }"
b2="$(batch_id "$out")"
wait_for 1 "lote 2 concluído (ms-api parado)" completed "$b2"
docker start ms-api >/dev/null
wait_for 200 "ms-api /readyz" status "$API/readyz"
curl -sN --max-time 4 -H "Authorization: Bearer $TOKEN" -H "Last-Event-ID: $L" "$API/events" > "$TMP/stream2" || true
got="$(grep '^id: ' "$TMP/stream2" | cut -d' ' -f2 | tr '\n' ' ')"
want="$(sql "SELECT string_agg(seq::text, ' ' ORDER BY seq) FROM events WHERE seq > $L") "
expect "ids depois do reinício == eventos do banco depois de $L" "$want" "$got"
expect "sem id repetido" 0 "$(grep '^id: ' "$TMP/stream2" | sort | uniq -d | wc -l | tr -d ' ')"
expect "batch.completed do lote 2 no stream" 1 "$(grep -c '^event: batch.completed' "$TMP/stream2")"
expect "Last-Event-ID inválido" 400 "$(status -H "Authorization: Bearer $TOKEN" -H 'Last-Event-ID: abc' "$API/events")"

echo "== 5. leitura, miniatura com ETag e paginação"
ids="$(api "$API/photos?batch_id=$b1" | jget '" ".join(p["id"]+":"+p["status"] for p in d["items"])')"
done_id=""; failed_id=""
for kv in $ids; do case "${kv##*:}" in done) done_id="${kv%%:*}" ;; failed) failed_id="${kv%%:*}" ;; esac; done
[ -n "$done_id" ] && ok "foto done $done_id" || fail "lote 1 sem foto done"
[ -n "$failed_id" ] && ok "foto failed $failed_id" || fail "lote 1 sem foto failed"
etag="$(curl -si -H "Authorization: Bearer $TOKEN" "$API/photos/$done_id/thumb" | tr -d '\r' | awk -F': ' 'tolower($1)=="etag"{print $2}')"
[ -n "$etag" ] && ok "miniatura com ETag $etag" || fail "miniatura sem ETag"
expect "miniatura com If-None-Match" 304 "$(status -H "Authorization: Bearer $TOKEN" -H "If-None-Match: $etag" "$API/photos/$done_id/thumb")"
expect "miniatura da foto failed" 404 "$(status -H "Authorization: Bearer $TOKEN" "$API/photos/$failed_id/thumb")"
expect "lote 1: total/completed/failed" "2/2/1" "$(api "$API/batches/$b1" | jget '"/".join(str(d[k]) for k in ("total", "completed_count", "failed_count"))')"
expect "GET /batches?limit=1 tem next_cursor" true "$(api "$API/batches?limit=1" | jget 'str(d["next_cursor"] is not None).lower()')"

echo "== 6. DLQ com a causa e retry sem duplicar resultado"
expect "causa na DLQ" "HTTP 404" "$(api "$API/dlq?limit=100" | jget '[p["failure_reason"] for p in d["items"] if p["id"]=="'"$failed_id"'"][0]')"
expect "retry da foto failed" 202 "$(status -X POST -H "Authorization: Bearer $TOKEN" "$API/dlq/$failed_id/retry")"
wait_for failed "foto volta à DLQ (a origem continua 404)" photo_status "$failed_id"
sleep 2
expect "completed_count do lote 1 intocado" 2 "$(sql "SELECT completed_count FROM batches WHERE id='$b1'")"
expect "batch.completed do lote 1 não reemitido" 1 "$(sql "SELECT count(*) FROM outbox WHERE routing_key='batch.completed' AND payload->>'batch_id'='$b1'")"
expect "evento photo.retried gravado" 1 "$(sql "SELECT count(*) FROM events WHERE type='photo.retried' AND photo_id='$failed_id'")"
expect "retry de foto done" 409 "$(status -X POST -H "Authorization: Bearer $TOKEN" "$API/dlq/$done_id/retry")"
expect "retry de foto inexistente" 404 "$(status -X POST -H "Authorization: Bearer $TOKEN" "$API/dlq/00000000-0000-4000-8000-000000000000/retry")"

echo "== 7. SIGTERM com stream aberto: saída 0"
curl -sN -H "Authorization: Bearer $TOKEN" "$API/events" > "$TMP/stream3" &
curl_pid=$!
sleep 1
docker stop -t 15 ms-api >/dev/null
expect "saída do ms-api" 0 "$(docker inspect -f '{{.State.ExitCode}}' ms-api)"
if grep -q '^event: shutdown' "$TMP/stream3"; then ok "stream recebeu event: shutdown"; else fail "stream sem event: shutdown"; fi
docker start ms-api >/dev/null
wait_for 200 "ms-api /readyz" status "$API/readyz"

echo
if [ "$fails" -eq 0 ]; then echo "ACEITE DA FASE 6: OK"; else echo "ACEITE DA FASE 6: $fails FALHA(S)"; exit 1; fi
