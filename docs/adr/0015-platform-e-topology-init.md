# ADR 0015: Código compartilhado (platform) e dono único da topologia

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** D1, G4, Fase 1 (`docs/plan-go.md`)

## Contexto

Argumentos divergentes de filas entre serviços causam `PRECONDITION_FAILED`; código compartilhado demais acopla os serviços.

## Decisão

`platform` contém só config, `slog`, conexão AMQP com reconexão e tipos de mensagem. Serviços não importam uns aos outros (G4, por teste ou `depguard`). Um comando `topology-init` (no boot do `ms-queues`) declara exchange, filas, degraus de retry, DLQ e `batch.completed`; os demais só verificam com `QueueDeclarePassive` e falham o `/readyz` se divergir.

## Alternativas

- Cada serviço declara as suas filas (conflito de argumentos).
- Duplicar tipos de mensagem em cada serviço (drift de contrato).

## Custo

`platform` vira ponto de acoplamento: mudança nele exige `/code-review` `high`.

## Critério de reversão

Se `platform` crescer além do núcleo listado, dividir ou duplicar o excedente.

## Evidência

Pendente. Fase 1: serviço com topologia divergente falha o `/readyz` em vez de recriá-la.
