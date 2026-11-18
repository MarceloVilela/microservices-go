# S2: retry por degraus (TTL fixo + DLX) × retry atrasado nativo das quorum queues

Executado em 2026-11-17. Código em `specs/spikes/s2-retry/` (`main.go`, módulo isolado). Saídas brutas: `s2-resultado-ttl.txt` e `s2-resultado-nativo.txt` (esta última contém ~220 linhas por causa dos laços de `nack`).

**Ambiente:** o mesmo do S1 (Go 1.27.4, `amqp091-go` v1.17.0, RabbitMQ **4.3.6** em container local, `rabbitmq:4-management-alpine`, `--user rabbitmq`). Uma execução de cada experimento; sem carga concorrente.

## A. Degraus TTL + DLX (ordem, backoff, DLQ)

Topologia: `s2.process` → (falha transitória) → `s2.retry.N` (TTL fixo **na fila**, DLX de volta para `s2.process`) → depois de esgotar os degraus, `s2.dlq`. O worker publica no degrau com confirm e só então dá ack.

| Degrau (TTL configurado) | Atraso medido |
|---|---|
| 5 s | **5,003 s** |
| 30 s | **30,004 s** |
| Total até a DLQ | 35,009 s |

O backoff por degraus funciona como o plano descreve, com erro de poucos milissegundos, e a mensagem termina na DLQ depois do último degrau.

## B. *Thundering herd* (jobs que falharam juntos voltam juntos)

200 mensagens falham no mesmo instante e entram num degrau de 2 s:

- atrasos medidos: min 2,001 s, p50 2,002 s, máx 2,004 s;
- janela de chegada de todas as 200 reentregas: **196 ms**;
- pico: **105 das 200 reentregas dentro de uma única janela de 100 ms**.

O herd é **real e mensurável** (confirma a hipótese do plano). Como o TTL é fixo por fila, não há jitter.

## C. Bloqueio de cabeça de fila (TTL por mensagem em fila única)

Mensagem A com `expiration` de 6000 ms publicada antes da B, de 1000 ms, na mesma fila: **as duas chegaram à DLQ aos 6 s**. B esperou A. Confirma a ressalva da documentação e justifica **uma fila por degrau com TTL na fila** (ADR 0005). (Testado em fila clássica.)

## D. Retry atrasado nativo da quorum queue (disponível no RabbitMQ 4.3.6)

Declaração testada: `x-queue-type=quorum`, `x-delayed-retry-type` (`all`, `failed` ou `returned`), `x-delayed-retry-min=500`, `x-delayed-retry-max=3000`, `x-delivery-limit=5`, DLX para `s2.nativedlq`. O consumidor devolve a mensagem sempre.

| Tipo | Devolução | Resultado medido |
|---|---|---|
| `all` | `basic.reject` (requeue) | atrasos **0,51 s, 1,14 s, 1,55 s, 2,04 s, 2,54 s** (linear, ≈ `min × delivery-count`); `x-delivery-count` 1 a 5; **DLQ em 7,8 s** pelo `x-delivery-limit` |
| `failed` | `basic.reject` (requeue) | idêntico ao anterior (0,52 s a 2,51 s; DLQ em 7,67 s) |
| `all` | `basic.nack` (requeue) | atraso **fixo ≈ mínimo** (0,55 a 0,68 s); `x-delivery-count` **nunca incrementa**; **nunca chega à DLQ** (≈100 entregas em 60 s até o timeout do teste) |
| `returned` | `basic.nack` (requeue) | idem: atraso ≈ mínimo, sem contagem, sem DLQ |
| `disabled` (controle) | `basic.reject` (requeue) | reentrega em ~10 ms; DLQ em 50 ms |

Pontos observados:

- O retry nativo **funciona** na versão do broker usada e o argumento `x-delayed-retry-*` é aceito.
- O atraso é **linear** (`min × delivery-count`, com teto `max`) e **o mesmo para todas as mensagens**: não há como pedir "espere 45 s" para uma mensagem. Não reproduz a escada de 5 s, 30 s, 2 min e 5 min, nem o `Retry-After` arredondado ao degrau.
- **`basic.nack` com requeue não incrementa o contador**: o atraso não cresce e o `x-delivery-limit` nunca é atingido, então a mensagem circula **para sempre**. Para ter backoff e DLQ, o consumidor precisa usar `basic.reject` (ou `modified` com `delivery_failed`, que não foi testado).
- Também **não há jitter** (o atraso é determinístico por contagem), então o herd é equivalente.
- A documentação consultada (via leitura da página, não confirmada por mim em texto integral) coloca o recurso em RabbitMQ **4.3 ou superior** e só em quorum queues; o experimento confirma a disponibilidade em 4.3.6.

## Conclusões para a ADR 0005 (decisão **não** tomada; depende da sua aprovação)

| Critério | TTL + DLX (escada) | Retry nativo (quorum) |
|---|---|---|
| Funciona no 4.3.6 | sim | sim |
| Escada 5 s, 30 s, 2 min, 5 min | sim, exata | não (linear com teto) |
| `Retry-After` arredondado ao degrau | sim | não (atraso único por contagem) |
| Herd/jitter | herd medido (105/200 em 100 ms), sem jitter | sem jitter, também determinístico |
| Filas e topologia | 1 fila por degrau + DLQ | 1 fila só, `delivery-limit` → DLQ |
| Janela de duplicata | publica no degrau e depois dá ack (queda no meio duplica; o worker é idempotente) | devolução é do broker, sem republicação |
| Armadilha | nenhuma encontrada | `nack` não conta: loop infinito sem DLQ |
| Custo de quorum | não se aplica (fila clássica; ver S1) | latência de confirm de quorum **não medida** aqui |

**Leitura:** como o plano exige `Retry-After` respeitado por degrau (aceite da Fase 4: "`Retry-After` de 45 s espera o degrau de 2 min"), o nativo **não atende** esse requisito; só o atenderia se o requisito fosse relaxado. A escada TTL + DLX atende e o herd, embora real, é aceitável para o volume do projeto (centenas de jobs). A recomendação é **manter a escada TTL + DLX** e registrar o herd medido como custo assumido; o retry nativo fica como alternativa documentada (e o atalho `reject` × `nack` entra como armadilha na ADR). A decisão final é sua.

**Decisão (2026-11-17):** o usuário escolheu primeiro o retry nativo e, depois de ver a comparação com a latência de quorum medida (S1), **voltou para a escada TTL + DLX**. A tabela comparativa final está na ADR 0005.

## Limites desta evidência

- Uma execução de cada; localhost; sem carga concorrente além das 200 mensagens do herd.
- A escada real foi testada só com **2 degraus** (5 s, 30 s); 2 min e 5 min não foram esperados (a mecânica é a mesma, só o TTL muda).
- O TTL + DLX foi testado em fila **clássica**; em quorum queues a expiração por TTL pode ter outras propriedades (não testado).
- Latência de confirm de quorum queues não medida (o S1 foi só com fila clássica).
- O modo `modified` com `delivery_failed` (AMQP 1.0) e o `x-opt-delivery-time` não foram testados.
