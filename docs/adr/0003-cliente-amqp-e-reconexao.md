# ADR 0003: Cliente AMQP e reconexão

- **Status:** aceita (aprovada pelo usuário em 2026-11-17)
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

Parcial. **S1 executado em 2026-11-17** (`specs/spikes/s1-amqp.md`): reconexão com backoff após `docker restart` do broker funcionou (200 confirmadas, 1 reenvio, 7 tentativas de conexão). Só broker reiniciado, fila clássica, uma execução, com um protótipo de spike (não o código de `platform`). O critério de reversão não foi acionado. **Fase 4 (2026-11-18, `specs/execucao-fase-4.log`):** reconexão do **consumidor** com `Supervisor` (backoff 1 s a `CONSUMER_BACKOFF_MAX`, reinicia após sessão estável de 30 s): o mesmo processo do worker volta a consumir após queda de conexão e após `docker restart` do broker real; boot com o broker fora espera em vez de sair; `Ack` que falha por canal fechado não derruba o serviço (reentrega absorvida pela ADR 0006). **Não coberta por teste de integração:** queda só do canal.
