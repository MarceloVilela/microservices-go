#!/usr/bin/env bash
# Aceite da Fase 3 (ms-worker e fim de lote), ponta a ponta, com o Lorem Picsum de verdade.
# Requer a pilha de pé (./run.sh) e acesso à internet. Para e religa o ms-worker.
# Sai com 0 só se todos os critérios passarem. Uso: scripts/aceite-fase-3.sh
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck disable=SC1091
. ./.env
MGMT="http://localhost:15672/api"
AUTH="$RABBITMQ_USER:$RABBITMQ_PASS"
INGEST="http://localhost:8081"
WORKER="http://localhost:8083"
RUN="$(date +%s)-$$"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
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
qcount() { curl -s -u "$AUTH" "$MGMT/queues/%2F/$1" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("messages_ready",0))'; }
status() { curl -s -o /dev/null -w '%{http_code}' "$@" || true; }
post() { # chave json -> "corpo status"
  curl -s -w ' %{http_code}' -X POST "$INGEST/batches" -H 'Content-Type: application/json' -H "Idempotency-Key: $1" -d "$2" --max-time 5 || true
}
batch_id() { printf '%s' "${1% *}" | python3 -c 'import sys,json; print(json.load(sys.stdin)["batch_id"])'; }
completed() { sql "SELECT count(*) FROM batches WHERE id='$1' AND completed_at IS NOT NULL"; }
# dimensões da miniatura, lida do arquivo dentro do volume (a imagem final não tem shell)
thumb_dims() { # photo_id
  docker cp "ms-worker:/data/thumbs/$1.jpg" "$TMP/$1.jpg" >/dev/null 2>&1 || { echo ausente; return; }
  python3 - "$TMP/$1.jpg" <<'PY'
import struct, sys
b = open(sys.argv[1], 'rb').read()
if b[:2] != b'\xff\xd8' or b[-2:] != b'\xff\xd9':
    print('truncada'); sys.exit()
i = 2
while i < len(b):
    if b[i] != 0xFF: break
    m = b[i + 1]
    n = struct.unpack('>H', b[i + 2:i + 4])[0]
    if m in (0xC0, 0xC1, 0xC2):
        h, w = struct.unpack('>HH', b[i + 5:i + 9]); print(f'{w}x{h}'); sys.exit()
    i += 2 + n
print('sem SOF')
PY
}

echo "== 1. worker pronto (broker, Postgres, esquema e diretório de miniaturas)"
docker start ms-worker >/dev/null 2>&1 || true   # o aceite da Fase 2 o deixa parado
wait_for 200 "ms-worker /readyz" status "$WORKER/readyz"
wait_for 200 "ms-ingest /readyz" status "$INGEST/readyz"
# o aceite da Fase 2 deixou mensagens na fila; espera o worker esvaziá-la para contar só as deste aceite
wait_for 0 "photo.process vazia" qcount photo.process

echo "== 2. lote do dataset: 3 fotos válidas + 1 inexistente (404) termina e emite batch.completed uma vez"
dlq0="$(qcount photo.process.dlq)"; bc0="$(qcount batch.completed)"
body='{"photos":[{"source_url":"https://picsum.photos/id/10/1200/800"},{"source_url":"https://picsum.photos/id/20/1200/800"},{"source_url":"https://picsum.photos/id/30/1200/800"},{"source_url":"https://picsum.photos/id/9999/1200/800"}]}'
out="$(post "aceite3-$RUN-a" "$body")"
expect "POST /batches" 202 "${out##* }"
id="$(batch_id "$out")"
wait_for 1 "lote terminado (completed_at)" completed "$id"
expect "fotos done" 3 "$(sql "SELECT count(*) FROM photos WHERE batch_id='$id' AND status='done'")"
expect "fotos failed" 1 "$(sql "SELECT count(*) FROM photos WHERE batch_id='$id' AND status='failed'")"
expect "causa da falha" "HTTP 404" "$(sql "SELECT failure_reason FROM photos WHERE batch_id='$id' AND status='failed'")"
expect "completed_count" 4 "$(sql "SELECT completed_count FROM batches WHERE id='$id'")"
for pid in $(sql "SELECT id FROM photos WHERE batch_id='$id' AND status='done' ORDER BY id"); do
  expect "miniatura $pid" 200x133 "$(thumb_dims "$pid")"
done
expect "batch.completed no outbox (uma vez)" 1 "$(sql "SELECT count(*) FROM outbox WHERE routing_key='batch.completed' AND payload->>'batch_id'='$id'")"
wait_for "$((bc0 + 1))" "batch.completed publicado na fila (Ready, ainda sem consumidor)" qcount batch.completed
wait_for "$((dlq0 + 1))" "falha definitiva foi à DLQ" qcount photo.process.dlq
expect "eventos photo.done" 3 "$(sql "SELECT count(*) FROM events WHERE batch_id='$id' AND type='photo.done'")"
expect "eventos photo.failed" 1 "$(sql "SELECT count(*) FROM events WHERE batch_id='$id' AND type='photo.failed'")"
expect "eventos batch.completed" 1 "$(sql "SELECT count(*) FROM events WHERE batch_id='$id' AND type='batch.completed'")"

echo "== 3. O3 (consumidor parado): mensagens ficam Ready; ao voltar o worker, o lote termina"
docker stop ms-worker >/dev/null
out2="$(post "aceite3-$RUN-b" '{"photos":[{"source_url":"https://picsum.photos/id/40/1200/800"},{"source_url":"https://picsum.photos/id/50/1200/800"}]}')"
expect "POST com o worker parado" 202 "${out2##* }"
id2="$(batch_id "$out2")"
wait_for 2 "photo.process com 2 Ready (worker parado)" qcount photo.process
expect "lote ainda aberto" 0 "$(completed "$id2")"
docker start ms-worker >/dev/null
wait_for 1 "lote terminado depois que o worker voltou" completed "$id2"
expect "fotos done" 2 "$(sql "SELECT count(*) FROM photos WHERE batch_id='$id2' AND status='done'")"
wait_for 0 "photo.process vazia" qcount photo.process

echo "== 4. config check do ms-worker (DATABASE_URL obrigatória; nenhum valor é impresso)"
set +e
docker run --rm -e AMQP_URL=amqp://u:p@h/ ms-worker-app config check >/dev/null 2>&1; expect "sem DATABASE_URL sai 1" 1 "$?"
out="$(docker run --rm -e AMQP_URL=amqp://u:senha-secreta@h/ -e DATABASE_URL=postgres://u:senha-do-banco@h/d ms-worker-app config check 2>&1)"; code=$?
expect "com AMQP_URL e DATABASE_URL sai 0" 0 "$code"
case "$out" in *senha-secreta*|*senha-do-banco*) fail "config check imprimiu um segredo" ;; *) ok "config check não imprime segredos" ;; esac
docker run --rm -e AMQP_URL=amqp://u:p@h/ -e DATABASE_URL=postgres://u:p@h/d -e WORKER_CONCURRENCY=0 ms-worker-app config check >/dev/null 2>&1; expect "WORKER_CONCURRENCY=0 sai 1" 1 "$?"
set -e

echo
if [ "$fails" -eq 0 ]; then echo "ACEITE DA FASE 3: OK"; else echo "ACEITE DA FASE 3: $fails FALHA(S)"; exit 1; fi
