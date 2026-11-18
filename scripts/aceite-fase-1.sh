#!/usr/bin/env bash
# Aceite da Fase 1, ponta a ponta. Requer a pilha de pé (./run.sh).
# Sai com 0 só se todos os critérios passarem. Uso: scripts/aceite-fase-1.sh
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck disable=SC1091
. ./.env
MGMT="http://localhost:15672/api"
AUTH="$RABBITMQ_USER:$RABBITMQ_PASS"
fails=0

ok()   { printf 'ok    %s\n' "$1"; }
fail() { printf 'FALHA %s\n' "$1"; fails=$((fails + 1)); }
code() { curl -s -o /dev/null -w '%{http_code}' "$@" || true; }
expect() { # descrição esperado obtido
  if [ "$2" = "$3" ]; then ok "$1 ($3)"; else fail "$1: esperado $2, obtido $3"; fi
}
retry_code() { # url esperado: tenta por até 20 s (readyz abre uma conexão por chamada)
  local got=""
  for _ in $(seq 20); do
    got="$(code "$1")"; [ "$got" = "$2" ] && break; sleep 1
  done
  printf '%s' "$got"
}

echo "== 1. serviços saudáveis e prontos"
for s in ms-ingest:8081 ms-api:8082 ms-worker:8083 ms-notification:8084; do
  n=${s%%:*}; p=${s##*:}
  expect "$n /healthz" 200 "$(code "localhost:$p/healthz")"
  expect "$n /readyz" 200 "$(retry_code "localhost:$p/readyz" 200)"
  expect "$n docker health" healthy "$(docker inspect -f '{{.State.Health.Status}}' "$n")"
done

echo "== 2. topologia vista pelo broker (criada só pelo topology-init)"
expect "exchange photos (direct, durável)" "direct true" \
  "$(curl -s -u "$AUTH" "$MGMT/exchanges/%2F/photos" | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d["type"], str(d["durable"]).lower())')"
queues="$(curl -s -u "$AUTH" "$MGMT/queues/%2F" | python3 -c 'import sys,json; print(" ".join(sorted(q["name"] for q in json.load(sys.stdin))))')"
expect "7 filas" "batch.completed photo.process photo.process.dlq photo.process.retry.2m photo.process.retry.30s photo.process.retry.5m photo.process.retry.5s" "$queues"

echo "== 3. topologia divergente: /readyz falha e NADA é recriado"
curl -s -u "$AUTH" -X DELETE "$MGMT/queues/%2F/photo.process.dlq" -o /dev/null
expect "ms-api (exige a DLQ) /readyz" 503 "$(retry_code localhost:8082/readyz 503)"
expect "ms-worker (exige a DLQ) /readyz" 503 "$(retry_code localhost:8083/readyz 503)"
expect "ms-ingest (não exige a DLQ) /readyz" 200 "$(code localhost:8081/readyz)"
expect "ms-notification (não exige a DLQ) /readyz" 200 "$(code localhost:8084/readyz)"
expect "ms-api continua 503 na 2ª chamada" 503 "$(code localhost:8082/readyz)"
expect "DLQ continua ausente (nenhum serviço recriou)" 404 "$(code -u "$AUTH" "$MGMT/queues/%2F/photo.process.dlq")"
echo "-- recuperação: só o topology-init recria"
./run.sh ms-queues run --rm topology-init >/dev/null 2>&1 || fail "topology-init"
expect "ms-api /readyz depois do topology-init" 200 "$(retry_code localhost:8082/readyz 200)"
expect "ms-worker /readyz depois do topology-init" 200 "$(retry_code localhost:8083/readyz 200)"

echo "== 4. config check e healthcheck dentro da imagem final"
set +e
docker run --rm ms-ingest-app config check >/dev/null 2>&1; expect "config check sem AMQP_URL sai 1" 1 "$?"
docker run --rm -e AMQP_URL=amqp://u:p@h/ ms-ingest-app config check >/dev/null 2>&1; expect "config check válido sai 0" 0 "$?"
docker run --rm -e 'http-addr=:1' -e AMQP_URL=amqp://u:p@h/ ms-ingest-app config check >/dev/null 2>&1; expect "nome de variável inválido sai 1" 1 "$?"
docker run --rm ms-ingest-app healthcheck >/dev/null 2>&1; expect "healthcheck sem servidor sai 1" 1 "$?"
set -e

echo "== 5. download HTTPS na base da imagem final (distroless/static:nonroot)"
docker build -q --target https-probe -t ms-go-https-probe . >/dev/null
if out="$(docker run --rm ms-go-https-probe https://picsum.photos/id/1/64/64 2>&1)"; then ok "$out"; else fail "https-probe: $out"; fi

echo
if [ "$fails" -eq 0 ]; then echo "ACEITE DA FASE 1: OK"; else echo "ACEITE DA FASE 1: $fails falha(s)"; exit 1; fi
