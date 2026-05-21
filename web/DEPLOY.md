# Deploy do painel web (Vercel)

O painel (`web/`, Next.js App Router) é hospedado na **Vercel**. A landing
estática (`ItacitrusDev/assistente-landing`, Cloudflare Pages) fica **intocada**
— ela serve a revisão do Google OAuth e não deve virar app com build.

## 1. Conectar o projeto na Vercel

- Importar o repo `itacitrus/assistente_pessoal` na Vercel.
- **Root Directory: `web`** (monorepo — o app vive no subdiretório, não na raiz).
- Framework preset: Next.js (auto-detectado). Build: `next build`. Sem overrides.

## 2. Variável de ambiente (Production + Preview)

| Var | Valor |
|-----|-------|
| `NEXT_PUBLIC_API_BASE_URL` | `https://api.itacitrus.com.br/assistente` |

(O cliente prepende isso a `/api/v1/...`. A API do bot está montada sob
`/assistente` no ALB compartilhado — vide `bot/api/server.go` + `API_PATH_PREFIX`.)

## 3. Domínio custom — OBRIGATÓRIO pro login funcionar

O cookie de sessão é `SameSite=Strict`. Se o app ficar em `*.vercel.app`
(cross-site com `api.itacitrus.com.br`), o cookie **não é enviado** e o login
quebra. Solução: domínio sob `itacitrus.com.br`.

- Na Vercel: adicionar domínio custom, ex: **`painel.itacitrus.com.br`**.
- No Cloudflare (DNS de itacitrus.com.br): criar o CNAME que a Vercel indicar
  (`cname.vercel-dns.com`), modo **DNS only** (cinza, não proxied) pra Vercel
  emitir o cert.
- Resultado: `painel.itacitrus.com.br` (app) e `api.itacitrus.com.br` (API) são
  same-site → cookie flui, `SameSite=Strict` sem mudança de código.

> Alternativa (se não quiser subdomínio): trocar o cookie pra `SameSite=None;
> Secure` em `bot/api/middleware.go` (funciona em `.vercel.app`, CSRF passa a
> depender só do Origin check). Não recomendado.

## 4. Atualizar env do bot (instância EC2) pro domínio escolhido

O bot precisa saber a origem do painel pra (a) montar o magic link e (b) liberar
CORS. Hoje estão apontando pra `assistente.itacitrus.com.br` (placeholder — a
landing estática, que NÃO tem a página `/auth/verify`). Trocar pra o subdomínio
do painel:

```bash
# via SSM na instância i-0ce25dde7d35adf13, em /opt/assistente/.env:
WEB_BASE_URL=https://painel.itacitrus.com.br   # onde vive /auth/verify (magic link)
WEB_ORIGIN=https://painel.itacitrus.com.br     # CORS allowlist
# depois: docker compose up -d bot  (reinicia pra pegar env)
```

## 5. Ligar a landing ao painel

Na `assistente-landing`, o CTA "Login"/"Entrar na beta" deve linkar pra
`https://painel.itacitrus.com.br` (ou `/login`).

## 6. Fluxo de login (sanity após deploy)

1. Usuário abre `painel.itacitrus.com.br/login`, informa telefone.
2. Bot manda magic link no WhatsApp: `https://painel.itacitrus.com.br/auth/verify?token=...`.
3. Clica → `POST /assistente/api/v1/auth/verify` → cookie `lurch_session` setado.
4. Redireciona pra `/dashboard` → `GET /assistente/api/v1/me` autentica.
5. Responsável cria dependente (idoso) em `/dashboard/family/new`.

## Checklist

- [ ] Projeto Vercel com Root Directory = `web`
- [ ] `NEXT_PUBLIC_API_BASE_URL` setada
- [ ] Domínio custom sob itacitrus.com.br + CNAME no Cloudflare (DNS only)
- [ ] `WEB_BASE_URL` + `WEB_ORIGIN` do bot atualizados pro subdomínio
- [ ] Bot reiniciado
- [ ] Login end-to-end testado (magic link → dashboard)
