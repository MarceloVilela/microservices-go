# ADR 0010: Fonte de fotos, allowlist e SSRF completa

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** N4, Fase 7 (`docs/plan-go.md`)

## Contexto

O worker baixa URLs vindas do cliente; allowlist só no worker e só de host deixa SSRF e exaustão de memória em aberto (redirect, DNS rebinding, PNG pequeno com dimensões enormes).

## Decisão

Fonte: Lorem Picsum (a confirmar). Allowlist de hosts **no ingest (`400`) e no worker**; `CheckRedirect` reaplicando a allowlist; checagem do **IP resolvido** (bloqueia loopback, link-local e redes privadas); timeout; limite de bytes; teto de pixels via `image.DecodeConfig` antes de decodificar. Host fora da allowlist é falha **definitiva** (DLQ direta).

## Alternativas

- Fonte alternativa: Pexels.
- Só limite de bytes (não pega PNG pequeno com dimensões enormes).

## Custo

Resolver e validar o IP a cada conexão e cada redirect é mais código e mais teste.

## Critério de reversão

S3 mostrar que a API do Picsum não atende (ver ADR 0001).

## Evidência

Pendente. S3 (a API foi recomendada de memória; **confirmar contrato e comportamento antes de aceitar esta ADR**) e o teste de entrada hostil da Fase 7.
