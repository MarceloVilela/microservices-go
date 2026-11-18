# S3: Lorem Picsum (respostas reais)

Executado em 2026-11-17T19:08Z com `curl` a partir da máquina local. Só leitura; nenhuma conta ou segredo.

## 1. Redirect em /id/{id}/{w}/{h} (sem seguir)
```
HTTP/2 302 
location: https://fastly.picsum.photos/id/10/1200/800.jpg?hmac=fFdxJSYetBnQ8BUWcEuy--QakfwvxN_D4WJj2RElm5w
cache-control: public, max-age=86400, stale-while-revalidate=60, stale-if-error=43200
```

## 2. Destino do redirect
```
https://fastly.picsum.photos/id/10/1200/800.jpg?hmac=fFdxJSYetBnQ8BUWcEuy--QakfwvxN_D4WJj2RElm5w
HTTP/2 200 
content-type: image/jpeg
cache-control: public, max-age=2592000, stale-while-revalidate=60, stale-if-error=43200, immutable
content-length: 140884
```

## 3. Id inexistente (9999) e id 86
```
id=9999
HTTP/2 404 
content-type: text/plain; charset=utf-8
Image does not exist
id=86
HTTP/2 404 
content-type: text/plain; charset=utf-8
Image does not exist
```

## 4. /v2/list
```
[{"id":"0","author":"Alejandro Escamilla","width":5000,"height":3333,"url":"https://unsplash.com/photos/yC-Yzbqy7PY","download_url":"https://picsum.photos/id/0/5000/3333"},{"id":"1","author":"Alejandro Escamilla","width":5000,"height":3333,"url":"https://unsplash.com/photos/LNRyGwIJr5c","download_url":"https://picsum.photos/id/1/5000/3333"},{"id":"2","author":"Alejandro Escamilla","width":5000,"height":3333,"url":"https://unsplash.com/photos/N7XodRrbzS0","download_url":"https://picsum.photos/id/2/5000/3333"}]

```

## 5. Ids válidos de amostra (HTTP final após seguir redirect)
```
id=10 code=200 size=140884 type=image/jpeg
id=20 code=200 size=105446 type=image/jpeg
id=30 code=200 size=83313 type=image/jpeg
id=40 code=200 size=108677 type=image/jpeg
id=50 code=200 size=158635 type=image/jpeg
id=60 code=200 size=57921 type=image/jpeg
```

## Conclusões (para a ADR 0010)

- **Contrato confirmado:** `/id/{id}/{w}/{h}` responde `302` para `fastly.picsum.photos` (com `hmac` na URL) e o destino devolve `200 image/jpeg`. `/v2/list` devolve JSON com `id`, `author`, `width`, `height`, `url` e `download_url`.
- **A allowlist precisa cobrir o redirect:** além de `picsum.photos`, o `CheckRedirect` precisa aceitar `fastly.picsum.photos` (e revalidar o IP resolvido a cada salto). Confirma o achado 5(a) do code review do plano.
- **Id inexistente:** `404` com corpo `text/plain` "Image does not exist" (ids `9999` e `86`) → falha **definitiva**.
- **Cache:** o redirect tem `max-age=86400`; o destino é `immutable` com `max-age=2592000`.
- **Não verificado ainda:** rate limit, comportamento sob carga, `?grayscale`/`?blur` e variações de formato (`.webp`). `HEAD` devolve `405` em `picsum.photos` e `404` em `/v2/list`: o worker deve usar `GET`.
- **Veredito do S3:** o Lorem Picsum atende (ADR 0001 e 0010 podem avançar para `aceita` com a sua aprovação); o fallback Pexels não é necessário por ora.
