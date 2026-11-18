# ADR 0015: Código compartilhado (platform) e dono único da topologia

- **Status:** aceita (aprovada pelo usuário em 2026-11-18, com a evidência da Fase 1)
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

Fase 1 (2026-11-18, `specs/execucao-fase-1.log`): `topology-init` declara exchange + 7 filas (conferido pela API de gerenciamento) e é idempotente;
apagada a DLQ, `ms-api` e `ms-worker` dão `/readyz` 503, `ms-ingest` e `ms-notification` (que não a exigem) seguem 200, e a fila **continua ausente**
até o `topology-init` rodar de novo. `archtest` garante que só `ms-queues` e `platform/amqpx` chamam `ExchangeDeclare`/`QueueDeclare`/`QueueBind`.
**Limite:** a verificação passiva só detecta ausência, não argumentos divergentes (esses falham no `topology-init`).
