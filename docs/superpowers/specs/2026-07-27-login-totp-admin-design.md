# Login TOTP do admin (out-of-band do WhatsApp)

**Data:** 2026-07-27
**Status:** aprovado, em implementação

## Problema — deadlock de autenticação

O login do painel emite um magic link **pelo WhatsApp do bot**
(`/auth/request-link` → `SendMagicLink`). Quando o WhatsApp do bot cai — que é
exatamente quando o admin precisa entrar para reparear — o código não chega:
sem login não há acesso à página de pareamento, e sem pareamento o WhatsApp não
volta. Ciclo fechado.

## Objetivo

Um caminho de login do admin **independente do WhatsApp**, via **TOTP** (RFC
6238): código de 6 dígitos de um app autenticador (Google Authenticator/Authy),
gerado localmente a partir de um segredo compartilhado.

## Arquitetura

### Segredo (deploy config, fora do banco)

`ADMIN_TOTP_SECRET` (base32) em env — mesma filosofia de `ADMIN_PHONES`:
privilégio/segredo vivem na config de deploy, não em dado editável. Vazio ⇒
login TOTP desabilitado (handler responde 401 genérico). Um único segredo (há um
admin hoje); extensão futura para `phone:secret` fica fora de escopo.

### TOTP core (`bot/api/totp.go`)

Implementação stdlib (`crypto/hmac`+`crypto/sha1`+`encoding/base32`), validada
contra os vetores oficiais do RFC 6238 — evita dependência nova num caminho de
auth crítico e é totalmente testável.

- `GenerateTOTPSecret() (string, error)` — 20 bytes aleatórios → base32.
- `ValidateTOTP(secret, code string, now time.Time, skew int) (step int64, ok bool)`
  — SHA1, 6 dígitos, período 30s; testa janelas `-skew..+skew`; compara em tempo
  constante (`hmac.Equal`); devolve o passo de tempo casado (para anti-replay).
- `TOTPProvisioningURI(secret, account, issuer string) string` — `otpauth://`.

Parâmetros: **skew ±1 passo** (±30s, tolera drift de relógio do celular);
6 dígitos; período 30s (compatível com Google Authenticator).

### Endpoint `POST /api/v1/auth/admin-login`

Público (sem RequireAuth), com **RequireOrigin** (CSRF). Body `{phone, code}`:

1. normaliza o telefone; exige `phone ∈ ADMIN_PHONES`;
2. **rate limit** reusando o existente: `CountRecentLoginAttempts` (3/telefone/h)
   e `CountRecentLoginAttemptsByIP` (10/IP/h) + `RecordLoginAttempt`;
3. valida `code` contra `ADMIN_TOTP_SECRET` (skew ±1);
4. **anti-replay:** `Server` guarda o último passo consumido (int64, mutex);
   rejeita `step <= lastStep`;
5. tudo ok ⇒ `GetUserByPhone` (o admin precisa ser usuário registrado),
   `CreatePendingSession` + `ActivateSession` + `setSessionCookie` — **a mesma
   sessão** do magic link, nada muda downstream.

Sempre executa a validação TOTP mesmo quando o telefone não é admin ou o TOTP
está desabilitado (compara contra um segredo dummy) para equalizar timing e não
vazar se o telefone é admin. Erro genérico único: "Telefone ou código inválidos."

Auditoria: `admin_login_succeeded` / `admin_login_failed`
(reason: bad_code | not_admin | rate_limit | replay | disabled | no_user).

### CLI de enrollment — `bot admin-totp-setup`

Uma vez: gera o segredo, imprime **(a)** o valor para o `.env` de prod
(`ADMIN_TOTP_SECRET=...`), **(b)** o `otpauth://` URI e **(c)** um QR em ASCII no
terminal (renderizado com `rsc.io/qr`, já dependência). O admin roda, escaneia no
app, cola o segredo no `.env`, reinicia o bot. O segredo nunca passa pelo
assistente — quem roda é o operador.

### Frontend — página de login

Um link discreto **"Acesso do administrador"** revela campos telefone + código
de 6 dígitos → chama `adminLogin(phone, code)`. Genérico (qualquer um abre; só
admin + código certo entra) — não revela quais números são admin. Sucesso →
seta cookie e navega para `/dashboard` (igual ao verify).

## Segurança (é o caminho do maior privilégio)

- Brute force: espaço 10⁶ × rate limit 3/telefone/h ⇒ inviável.
- Replay: guard de último passo (TLS já protege em trânsito).
- Enumeração: erro genérico + validação em tempo constante + TOTP sempre
  executado (dummy quando desabilitado/não-admin).
- Sem session fixation: o servidor gera o token da sessão (não vem do cliente).
- HTTPS + cookie httpOnly/secure (já em prod).

## Testes

- `ValidateTOTP` contra vetores RFC 6238 (secret ASCII "12345678901234567890"):
  T=59→287082, T=1111111109→081804, T=1234567890→005924; skew ±1; código errado
  → ok=false; replay (mesmo step) tratado no handler.
- Handler: 400 (body inválido), 401 (não-admin, código errado, TOTP desabilitado),
  429 (rate limit), 401 replay (mesmo código 2×), 200 + cookie (sucesso).
- Enrollment: secret decodifica base32; URI bem formada.

## Fora de escopo (YAGNI)

- Múltiplos admins com secrets distintos (um admin hoje).
- "Lembrar dispositivo", backup codes (o magic-link via WhatsApp continua sendo
  o caminho normal quando o WhatsApp está no ar; o CLI regenera o secret se
  perder o app).
- Persistir o anti-replay entre reinícios (janela de 30s; risco desprezível).
