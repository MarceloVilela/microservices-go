# ADR 0003: Cliente AMQP e reconexão

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** D2 (`docs/plan-go.md`)

## Contexto

O v1 usava `php-amqplib` sem reconexão. Em Go, a reconexão é responsabilidade de quem chama o cliente.

## Decisão

`amqp091-go` (biblioteca mantida pelo time do RabbitMQ) com **camada própria de reconexão** em `platform`, com backoff e reabertura de canais e consumers.

## Alternativas

- Outro cliente AMQP para Go (verificar manutenção antes de considerar).
- Sem camada de reconexão (o serviço morre e o orquestrador reinicia); não atende N3 nem RQ5.

## Custo

Código e testes de reconexão são nossos; cobre canal, conexão e confirms.

## Critério de reversão

Biblioteca descontinuada ou bug bloqueante no spike.

## Evidência

Pendente. S1 (confirms e reconexão com RabbitMQ 4; 1.000 publicações com latência medida).
