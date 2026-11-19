#!/usr/bin/env bash
# Aceite da Fase 8 (painel Angular) do lado deste repositório.
# Sempre roda (só precisa do compose): origem estranha recusada e contrato igual ao do painel (se o repositório
# do painel existir ao lado; senão a seção é pulada e avisada).
# Com WITH_WEB=1 (exige `WITH_WEB=1 ./run.sh`): painel servido, CORS da origem do painel, nenhum token na imagem;
# com WITH_E2E=1 roda também o Playwright do painel (inclui derrubar/religar o ms-api).
# Sai com 0 só se tudo o que rodou passar. Uso: [WITH_WEB=1] [WITH_E2E=1] scripts/aceite-fase-8.sh
set -euo pipefail
cd "$(dirname "$0")/.."

# shellcheck disable=SC1091
. ./.env
WEB="http://localhost:8080"
INGEST="http://localhost:8081"
API="http://localhost:8082"
# mesma origem do build do compose (web/docker-compose.yml), para não conferir um painel e construir outro
PANEL="${PANEL_DIR:-$(cd web && cd "${WEB_CONTEXT:-../../microservices-frontend-angular}" 2>/dev/null && pwd || true)}"
fails=0

ok()   { printf 'ok    %s\n' "$1"; }
fail() { printf 'FALHA %s\n' "$1"; fails=$((fails + 1)); }
expect() { if [ "$2" = "$3" ]; then ok "$1 ($3)"; else fail "$1: esperado $2, obtido $3"; fi; }
# curl que nunca derruba o script (set -e): falha de rede vira texto vazio / 000 e a checagem reporta FALHA
status() { curl -s -o /dev/null -w '%{http_code}' "$@" 2>/dev/null || true; }
fetch() { curl -s "$@" 2>/dev/null || true; }
headers() { curl -s -D - -o /dev/null "$@" 2>/dev/null | tr -d '\r' | tr 'A-Z' 'a-z' || true; }

echo "== 1. origem estranha recusada pelo ms-api"
expect "ms-api recusa origem estranha" 403 "$(status -H 'Origin: http://evil.example' -H "Authorization: Bearer $API_DEMO_TOKEN" "$API/dlq")"

echo "== 2. contrato do painel = contrato deste repositório"
if [ -n "$PANEL" ] && [ -d "$PANEL/contract" ]; then
  for f in openapi-api.yaml openapi-ingest.yaml events.schema.json; do
    cmp -s "specs/$f" "$PANEL/contract/$f" && ok "contract/$f igual a specs/$f" || fail "contract/$f diverge de specs/$f (rode scripts/sync-contract.sh e npm run gen:contract no painel)"
  done
else
  echo "pulado: painel não encontrado (defina PANEL_DIR); a comparação com a tag contract-v1 só existe depois de publicá-la"
fi

if [ "${WITH_WEB:-0}" = 1 ]; then
  echo "== 3. painel servido pelo nginx"
  expect "GET /healthz" 200 "$(status "$WEB/healthz")"
  expect "GET / (index.html)" 200 "$(status "$WEB/")"
  expect "rota do Angular cai no index (SPA)" 200 "$(status "$WEB/painel/abc")"
  grep -q '<app-root' <<<"$(fetch "$WEB/")" && ok "index.html tem <app-root>" || fail "index.html sem <app-root>"
  expect "config.json sem cache" "cache-control: no-store" "$(headers "$WEB/config.json" | grep '^cache-control' || true)"
  grep -q 'apiUrl' <<<"$(fetch "$WEB/config.json")" && ok "config.json aponta os backends" || fail "config.json sem apiUrl"

  echo "== 4. CORS: a origem do painel passa"
  for u in "$API/events" "$API/dlq"; do
    h="$(headers -X OPTIONS "$u" -H "Origin: $WEB" -H 'Access-Control-Request-Method: GET' -H 'Access-Control-Request-Headers: authorization,last-event-id')"
    grep -q "^access-control-allow-origin: $WEB" <<<"$h" && ok "$u aceita $WEB" || fail "$u não aceitou $WEB"
    grep -q '^access-control-allow-headers:.*authorization' <<<"$h" && ok "$u permite Authorization" || fail "$u sem Authorization em Allow-Headers"
  done
  h="$(headers -X OPTIONS "$INGEST/batches" -H "Origin: $WEB" -H 'Access-Control-Request-Method: POST' -H 'Access-Control-Request-Headers: authorization,content-type,idempotency-key')"
  grep -q "^access-control-allow-origin: $WEB" <<<"$h" && ok "ingest aceita $WEB no POST /batches" || fail "ingest não aceitou $WEB"
  grep -q '^access-control-allow-headers:.*idempotency-key' <<<"$h" && ok "ingest permite Idempotency-Key" || fail "ingest sem Idempotency-Key em Allow-Headers"

  echo "== 5. nenhum token na imagem do painel"
  # sem o container (ou sem `sh`) não há o que conferir: isso é FALHA, não "ok"
  if docker exec web true >/dev/null 2>&1; then
    if docker exec web sh -c "grep -rl '$API_DEMO_TOKEN' /usr/share/nginx/html" 2>/dev/null | grep -q .; then fail "token do .env encontrado na imagem"; else ok "token ausente em /usr/share/nginx/html"; fi
  else
    fail "container web inacessível: não deu para conferir a imagem (rode WITH_WEB=1 ./run.sh)"
  fi

  if [ "${WITH_E2E:-0}" = 1 ]; then
    echo "== 6. ponta a ponta (Playwright, repositório do painel)"
    if (cd "$PANEL" && E2E_DOCKER=1 E2E_TOKEN="$API_DEMO_TOKEN" npx playwright test); then ok "Playwright verde"; else fail "Playwright falhou"; fi
  fi
else
  echo "(seções 3 a 6, do painel em nginx, puladas: rode com WITH_WEB=1 depois de WITH_WEB=1 ./run.sh)"
fi

echo
if [ "$fails" -eq 0 ]; then echo "ACEITE DA FASE 8: OK"; else echo "ACEITE DA FASE 8: $fails FALHA(S)"; exit 1; fi
