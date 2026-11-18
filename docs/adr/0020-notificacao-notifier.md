# ADR 0020: Notificação: interface Notifier e destinos

- **Status:** proposta
- **Data:** 2026-11-17
- **Origem no plano:** RQ4, N6, Fase 5 (`docs/plan-go.md`); decisão de descartar o Loggly (ADR 0009)

## Contexto

No v1, a falha do Loggly derrubava `/notification/send`. Descartado o Loggly, a notificação de fim de lote continua sendo um requisito (RQ4) e precisa de uma abstração que aceite mais de um destino sem acoplar o consumidor a nenhum deles. E-mail foi sugerido como destino para o portfólio.

## Decisão

Interface pequena `Notifier`, **definida no consumidor** (`ms-notification`), com destinos como adaptadores: `slog` (padrão, sempre ligado) e **SMTP** (opcional, ligado por variável de ambiente). No desenvolvimento, o SMTP aponta para o **Mailpit** em container, que captura os e-mails sem enviar nada. O consumo de `batch.completed` é idempotente por `batch_id` (um e-mail por lote) e a falha de um destino é registrada, mas nunca derruba o consumo nem impede os demais destinos. Hipótese: **não decidida** até a evidência e a sua aprovação.

## Alternativas

- Só `slog` (sem e-mail): mais simples, mas demonstra menos a abstração.
- Webhook em vez de SMTP: mais fácil de testar, menos reconhecível.
- Manter o Loggly: descartado (ADR 0009).
- Serviço de e-mail de terceiros (SES, SendGrid): segredo e custo sem ganho no escopo local.

## Custo

Uma interface e dois adaptadores a mais, um container (Mailpit) no compose de desenvolvimento e testes de idempotência com efeito colateral externo. Fora do escopo: e-mail real em produção (SPF, DKIM, entregabilidade), já que o projeto não tem deploy.

## Critério de reversão

Se o SMTP ou o Mailpit consumirem esforço desproporcional na Fase 5, cortar o adaptador de e-mail e ficar só com `slog`, mantendo a interface.

## Evidência

Pendente. Testes da Fase 5: reentregar o mesmo `batch.completed` gera um log e um e-mail (visível no Mailpit); com o SMTP fora do ar o fluxo continua e a falha fica registrada.
