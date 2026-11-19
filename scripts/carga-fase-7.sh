#!/usr/bin/env bash
# Fase 7, bloco D (G2): carga com números reais. Demora alguns minutos; não faz parte do `make accept`.
# Saída: specs/execucao-fase-7-carga.log (linhas "CARGA ..." do teste + tamanho das imagens).
set -euo pipefail
cd "$(dirname "$0")/.."
export PATH="$HOME/go-sdk/go/bin:$HOME/go/bin:$PATH"
log=specs/execucao-fase-7-carga.log

{
  echo "# carga da Fase 7 — $(date -Iseconds)"
  echo "## tamanho das imagens (G2)"
  make -s images >/dev/null || { echo "carga: make images falhou" >&2; exit 1; }
  docker image ls --format '{{.Repository}}\t{{.Size}}' | { grep -E '^(ms-(ingest|worker|notification|api)-app|ms-queues-topology-init)\b' || true; } | sort
  echo "## teste de carga"
} > "$log"

set +o pipefail
( cd tests && go test -tags 'integration load' -count=1 -timeout 40m -v -run '^TestCarga$' ./... ) 2>&1 | grep --line-buffered -E 'CARGA|^ *(---|ok|FAIL|PASS|panic)' >> "$log"; 
rc=${PIPESTATUS[0]}
set -o pipefail
cat "$log"
{ [ "$rc" -ne 0 ] || grep -q 'FAIL' "$log"; } && { echo "carga: falhou (go test saiu $rc)" >&2; exit 1; }
echo "carga: ok ($log)"
