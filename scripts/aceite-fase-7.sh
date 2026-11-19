#!/usr/bin/env bash
# Aceite da Fase 7 (métricas e entrada hostil), com o broker, o Postgres e o Lorem Picsum de verdade.
# Requer a pilha de pé (./run.sh) e internet. Sai com 0 só se todos os critérios passarem.
# A prova das classificações do worker (redirect, IP interno, corpo gigante, PNG de dimensões enormes)
# e da carga fica nos testes de integração e em `make load`: a imagem de produção não aceita loopback.
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck disable=SC1091
. ./.env
INGEST="http://localhost:8081"
API="http://localhost:8082"
WORKER="http://localhost:8083"
NOTIF="http://localhost:8084"
TOKEN="$API_DEMO_TOKEN"
RUN="$(date +%s)-$$"
fails=0

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
# valor da série (nome exato, com rótulos) no /metrics de uma base; vazio se ausente
metric() { curl -s "$1/metrics" | awk -v s="$2" '$1 == s {print $2}' | head -1; }
# o mesmo, como inteiro (contadores saem como "3" ou "3.0")
imetric() { metric "$@" | sed 's/\.0*$//'; }

echo "== 1. pilha pronta"
for u in "$INGEST" "$API" "$WORKER" "$NOTIF"; do wait_for 200 "$u/readyz" status "$u/readyz"; done

echo "== 2. /metrics responde nos 4 serviços, sem token"
for u in "$INGEST" "$API" "$WORKER" "$NOTIF"; do
  expect "GET $u/metrics" 200 "$(status "$u/metrics")"
  m="$(curl -s "$u/metrics" || true)" # captura inteira: `curl | grep -q` dá SIGPIPE (23) sob pipefail
  grep -q '^go_goroutines ' <<<"$m" && ok "$u: métricas do runtime Go" || fail "$u: sem go_goroutines"
  grep -q '^http_requests_total{' <<<"$m" && ok "$u: http_requests_total" || fail "$u: sem http_requests_total"
done
for s in outbox_pending outbox_quarantined outbox_oldest_pending_age_seconds; do
  [ -n "$(metric "$INGEST" "$s")" ] && ok "ingest expõe $s" || fail "ingest sem $s"
done
for s in 'queue_messages{queue="photo.process"}' 'queue_messages{queue="photo.process.dlq"}' 'queue_messages{queue="photo.process.retry.5s"}'; do
  [ -n "$(metric "$WORKER" "$s")" ] && ok "worker expõe $s" || fail "worker sem $s"
done
for s in 'photos_by_status{status="done"}' photos_stalled sse_clients sse_last_seq; do
  [ -n "$(metric "$API" "$s")" ] && ok "api expõe $s" || fail "api sem $s"
done

echo "== 3. entrada hostil recusada no ingest (400, nada gravado)"
before="$(sql "SELECT count(*) FROM batches")"
n=0
for url in 'https://example.com/foto.jpg' 'https://127.0.0.1/foto.jpg' 'https://169.254.169.254/latest/meta-data/' \
           'http://picsum.photos/id/1/10/10' 'file:///etc/passwd' 'https://user:senha@picsum.photos/x.jpg' 'https://picsum.photos.evil.com/x.jpg'; do
  n=$((n + 1))
  out="$(post "aceite7-hostil-$n-$RUN" "{\"photos\":[{\"source_url\":\"$url\"}]}")"
  expect "ingest recusa $url" 400 "${out##* }"
done
expect "nenhum lote gravado pelas entradas hostis" "$before" "$(sql "SELECT count(*) FROM batches")"

echo "== 4. métricas acompanham um lote real (1 done + 1 failed)"
done0="$(metric "$WORKER" 'photos_processed_total{outcome="done"}')"; done0="${done0:-0}"
dlq0="$(metric "$WORKER" 'photos_processed_total{outcome="dlq"}')"; dlq0="${dlq0:-0}"
not0="$(metric "$NOTIF" 'notifications_total{outcome="notified"}')"; not0="${not0:-0}"
out="$(post "aceite7-$RUN" '{"photos":[{"source_url":"https://picsum.photos/id/21/1200/800"},{"source_url":"https://picsum.photos/id/9999/1200/800"}]}')"
expect "POST /batches" 202 "${out##* }"
b="$(batch_id "$out")"
wait_for 1 "lote concluído" completed "$b"
sleep 6 # cache de 5 s das consultas ao banco e ao broker
expect "photos_processed_total done +1" "$((${done0%.*} + 1))" "$(imetric "$WORKER" 'photos_processed_total{outcome="done"}')"
expect "photos_processed_total dlq +1 (a 404 é definitiva)" "$((${dlq0%.*} + 1))" "$(imetric "$WORKER" 'photos_processed_total{outcome="dlq"}')"
notified() { imetric "$NOTIF" 'notifications_total{outcome="notified"}'; }
wait_for "$((${not0%.*} + 1))" "notifications_total notified +1" notified
c="$(curl -s "$WORKER/metrics" | awk '$1 == "photo_process_seconds_count{outcome=\"done\"}" {print $2}')"
[ -n "$c" ] && ok "histograma photo_process_seconds com amostras ($c)" || fail "photo_process_seconds sem amostras"
expect "queue_messages da fila principal" 0 "$(imetric "$WORKER" 'queue_messages{queue="photo.process"}')"
if [ "$(imetric "$API" 'photos_by_status{status="failed"}')" -ge 1 ]; then ok "photos_by_status failed >= 1"; else fail "photos_by_status failed"; fi

echo "== 5. cardinalidade: nenhum id vira rótulo"
for u in "$INGEST" "$API" "$WORKER" "$NOTIF"; do
  if curl -s "$u/metrics" | grep -Eq '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}'; then fail "$u: UUID em /metrics"; else ok "$u: sem UUID em /metrics"; fi
done

echo "== 6. a causa da falha definitiva aparece na DLQ da API"
expect "failure_class da 404" permanent "$(api "$API/dlq?limit=100" | jget '[p["failure_class"] for p in d["items"] if p["batch_id"]=="'"$b"'"][0]')"

echo
if [ "$fails" -eq 0 ]; then echo "ACEITE DA FASE 7: OK"; else echo "ACEITE DA FASE 7: $fails FALHA(S)"; exit 1; fi
