# Fase 2 — UI de cadastro, login e gestao familiar

> Plano tecnico auto-contido. Pre-requisito: Fase 1 mergeada (`users.user_type`,
> tabela `family_links` ja em producao).
>
> Stack: Next.js 14 App Router + TypeScript + Tailwind + shadcn/ui no front;
> bot Go expoe API REST nova em `/api/v1/*`; SQLite continua sendo o storage
> unico.

---

## 1. Objetivo e nao-objetivos

### Objetivo

Substituir o fluxo de onboarding manual via CLI (`bot add-user`) por um app web
publico, em portugues, que permita:

1. Visitante cadastrar conta propria (tipo `comum` ou `responsavel`).
2. Visitante autenticar via magic link enviado pelo proprio WhatsApp do bot
   (mesmo numero, mesma identidade — `phone_number` continua sendo a chave).
3. Responsavel cadastrar dependentes (`user_type = idoso`) e abrir um
   `family_link` entre os dois usuarios.
4. Responsavel e usuario comum editarem suas preferencias (timezone, daily
   summary, reminder window, auto-confirm timeout).
5. Responsavel editar preferencias de notificacao por dependente
   (`notify_on_medication_miss`, etc — colunas da `family_links` da Fase 1).

### Nao-objetivos

- **Dashboard de status do dependente** (medicamentos tomados, alertas em
  aberto, eventos do dia) — Fase 5.
- **Upload de receita / parsing de prescricao** — Fase 3.
- **Convidar dependente por SMS / link** — Fase 4 (esta fase, dependente e
  cadastrado pelo responsavel; o bot ainda nao envia onboarding pra ele).
- **i18n** — pt-BR fixo. Sem `next-intl`, sem chaves de traducao.
- **OAuth do Google Calendar pelo web** — segue no fluxo legado (link enviado
  pelo bot, callback em `/assistente/oauth/callback`). Nao vai pra
  `/api/v1/*`.
- **Painel admin** — fora de escopo.

---

## 2. Estrutura de diretorios

```
assistente_pessoal/
├── bot/                                 # Go (existente)
│   ├── api/                             # NOVO
│   │   ├── server.go                    # Cria mux com rotas /api/v1/*
│   │   ├── middleware.go                # CORS, RequireAuth, RateLimit
│   │   ├── auth_handlers.go             # request-link, verify, logout, me
│   │   ├── user_handlers.go             # PATCH /users/me
│   │   ├── family_handlers.go           # CRUD de dependentes + links
│   │   ├── magic_link.go                # geracao/hash de tokens
│   │   ├── sessions.go                  # CRUD em web_sessions
│   │   ├── ratelimit.go                 # bucket por phone em web_login_attempts
│   │   └── errors.go                    # JSON error envelope
│   ├── main.go                          # Adiciona api.Mount(mux)
│   ├── db.go                            # +migracoes web_sessions/login_attempts
│   ├── ...
│   └── go.mod
│
└── web/                                 # NOVO — projeto Next.js standalone
    ├── package.json
    ├── tsconfig.json
    ├── next.config.mjs
    ├── tailwind.config.ts
    ├── postcss.config.mjs
    ├── components.json                  # shadcn/ui config
    ├── .env.example
    ├── .env.local                       # (gitignored)
    ├── middleware.ts                    # auth guard pra /dashboard/*
    ├── app/
    │   ├── layout.tsx                   # html/body, fontes, providers
    │   ├── globals.css                  # tailwind base + tokens da marca
    │   ├── page.tsx                     # /  (landing)
    │   ├── signup/
    │   │   └── page.tsx
    │   ├── login/
    │   │   └── page.tsx
    │   ├── auth/
    │   │   └── verify/
    │   │       └── page.tsx             # client component, le ?token=
    │   └── dashboard/
    │       ├── layout.tsx               # nav lateral, logout
    │       ├── page.tsx                 # placeholder + lista dependentes
    │       ├── preferences/
    │       │   └── page.tsx
    │       └── family/
    │           ├── new/
    │           │   └── page.tsx
    │           └── [id]/
    │               └── preferences/
    │                   └── page.tsx
    ├── components/
    │   ├── ui/                          # shadcn/ui generated + nossos
    │   │   ├── button.tsx
    │   │   ├── input.tsx
    │   │   ├── label.tsx
    │   │   ├── card.tsx
    │   │   ├── select.tsx
    │   │   ├── PhoneInput.tsx           # mascara (XX) XXXXX-XXXX
    │   │   ├── CepInput.tsx             # mascara + ViaCEP
    │   │   └── TimezoneSelect.tsx
    │   ├── forms/
    │   │   ├── SignupForm.tsx
    │   │   ├── LoginForm.tsx
    │   │   ├── DependentForm.tsx
    │   │   ├── PreferencesForm.tsx
    │   │   └── NotifyPreferencesForm.tsx
    │   └── nav/
    │       └── DashboardNav.tsx
    ├── lib/
    │   ├── api.ts                       # fetch wrapper (credentials: include)
    │   ├── masks.ts                     # maskPhone, maskCep, onlyDigits
    │   ├── viacep.ts                    # parseCepLookup
    │   ├── session.ts                   # client helper pra ler /api/v1/me
    │   └── timezones.ts                 # lista de TZs america/*
    └── types/
        └── api.ts                       # tipos compartilhados com o backend
```

Observacao sobre monorepo: nao se usa workspace (`pnpm-workspace.yaml`,
turborepo, etc) nesta fase. `/web/` tem `package.json` proprio, deploy
independente em Vercel. `bot/` continua com Dockerfile proprio.

---

## 3. DDL completo

Adicionar no `bot/db.go` dentro de `(db *DB) migrate()`:

```sql
CREATE TABLE IF NOT EXISTS web_sessions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES users(id),
    token_hash      TEXT NOT NULL UNIQUE,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'active', 'revoked', 'expired')),
    expires_at      DATETIME NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    activated_at    DATETIME,
    last_used_at    DATETIME,
    revoked_at      DATETIME,
    ip              TEXT NOT NULL DEFAULT '',
    user_agent      TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_web_sessions_user_id
    ON web_sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_web_sessions_status_expires
    ON web_sessions(status, expires_at);

CREATE TABLE IF NOT EXISTS web_login_attempts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    phone_number TEXT NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_web_login_attempts_phone_time
    ON web_login_attempts(phone_number, created_at);
CREATE INDEX IF NOT EXISTS idx_web_login_attempts_ip_time
    ON web_login_attempts(ip, created_at);
```

### Estados do `web_sessions.status`

| status    | quando                                                        |
|-----------|---------------------------------------------------------------|
| `pending` | logo apos `POST /auth/request-link`, antes do clique          |
| `active`  | apos `POST /auth/verify` bem-sucedido                         |
| `revoked` | apos `POST /auth/logout` ou troca de credencial sensivel      |
| `expired` | preenchido por sweep periodico quando `expires_at < now()`    |

### Semantica do `expires_at`

- `pending` → `created_at + 15min` (validade do magic link).
- ao virar `active` → `last_used_at + 30 dias` (sliding window).
  Cada `RequireAuth` que valida a sessao bate `last_used_at = now()` e estende
  `expires_at = now() + 30d`.

### `token_hash`

Sempre `sha256(token_plaintext)` em hex lowercase. O plaintext **nunca** e
gravado. O cookie carrega o plaintext; o lookup e por hash.

---

## 4. API REST — spec

Base URL: `https://api.assistente.itacitrus.com.br` (mesmo host atual do bot).
Body em JSON. Erros em JSON `{ "error": { "code": "...", "message": "..." } }`.
Cookie de sessao: `assistente_session`, httpOnly, Secure, SameSite=strict,
Path=/, dominio sem leading dot.

### Tabela densa

| Metodo | Path                                          | Auth  | Body / Query                                                                    | 200 OK                                                        | Erros                                                                                  |
|--------|-----------------------------------------------|-------|---------------------------------------------------------------------------------|---------------------------------------------------------------|----------------------------------------------------------------------------------------|
| POST   | `/api/v1/auth/request-link`                   | nao   | `{ "phone": "5511999999999" }`                                                  | `{ "ok": true }` (resposta opaca, mesmo se phone nao existe)  | 400 invalid_phone, 429 rate_limited                                                    |
| POST   | `/api/v1/auth/verify`                         | nao   | `{ "token": "<plaintext>" }`                                                    | `{ "user": User }` + Set-Cookie                               | 400 invalid_token, 410 token_expired, 409 already_used                                 |
| POST   | `/api/v1/auth/logout`                         | sim   | —                                                                               | `{ "ok": true }` + Set-Cookie expirado                        | 401 unauthorized                                                                       |
| GET    | `/api/v1/me`                                  | sim   | —                                                                               | `User`                                                        | 401 unauthorized                                                                       |
| PATCH  | `/api/v1/users/me`                            | sim   | partial `User` (subset editavel)                                                | `User` atualizado                                             | 400 validation_error, 401 unauthorized                                                 |
| POST   | `/api/v1/family/dependents`                   | sim   | `{ "name", "phone", "relation", "timezone"? }`                                  | `{ "dependent": User, "link": FamilyLink }`                   | 400 validation_error, 401 unauthorized, 403 not_responsavel, 409 phone_already_in_use  |
| GET    | `/api/v1/family/dependents`                   | sim   | —                                                                               | `{ "dependents": [{ "user": User, "link": FamilyLink }] }`    | 401 unauthorized, 403 not_responsavel                                                  |
| PATCH  | `/api/v1/family/dependents/{id}`              | sim   | partial `User` (apenas name, timezone, daily_summary_time, reminder_before)     | `User` atualizado                                             | 401, 403, 404                                                                          |
| PATCH  | `/api/v1/family/links/{id}/notify`            | sim   | partial `FamilyLink.notify_*`                                                   | `FamilyLink` atualizado                                       | 401, 403, 404                                                                          |

### Tipos compartilhados

```ts
// web/types/api.ts
export type UserType = "comum" | "responsavel" | "idoso";

export interface User {
  id: number;
  phone_number: string;          // 13 digitos, sem mascara
  name: string;
  user_type: UserType;
  timezone: string;              // "America/Sao_Paulo"
  daily_summary_time: string;    // "07:00"
  weekly_summary_day: string;    // "sunday" | ...
  weekly_summary_time: string;   // "20:00"
  reminder_before: string;       // "1h" | "30m" | "2h"
  auto_confirm_timeout: string;  // "2h"
  google_connected: boolean;     // derivado de google_credentials != ""
  is_active: boolean;
  created_at: string;            // ISO8601
}

export interface FamilyLink {
  id: number;
  guardian_id: number;
  dependent_id: number;
  relation: string;                       // "filha", "filho", "esposa", ...
  notify_on_medication_miss: boolean;
  notify_on_event_miss: boolean;
  notify_on_calendar_change: boolean;
  notify_on_low_battery: boolean;
  daily_digest_time: string | null;       // "21:00" ou null
  created_at: string;
}
```

### Schemas das requisicoes

```ts
// POST /auth/request-link
{ phone: string }                // aceita com ou sem mascara; backend normaliza

// POST /auth/verify
{ token: string }                // plaintext UUIDv4-like, 32 bytes hex

// PATCH /users/me
{
  name?: string,                 // 2..80 chars
  timezone?: string,             // tem que estar em timezones.ts
  daily_summary_time?: string,   // HH:MM
  weekly_summary_day?: "sunday"|"monday"|...|"saturday",
  weekly_summary_time?: string,
  reminder_before?: "15m"|"30m"|"1h"|"2h"|"4h",
  auto_confirm_timeout?: "30m"|"1h"|"2h"|"4h"|"never"
}

// POST /family/dependents
{
  name: string,                  // 2..80
  phone: string,                 // unique entre todos os users
  relation: string,              // 2..30
  timezone?: string              // default "America/Sao_Paulo"
}

// PATCH /family/dependents/{id}
{ name?, timezone?, daily_summary_time?, reminder_before? }

// PATCH /family/links/{id}/notify
{
  notify_on_medication_miss?: boolean,
  notify_on_event_miss?: boolean,
  notify_on_calendar_change?: boolean,
  notify_on_low_battery?: boolean,
  daily_digest_time?: string|null
}
```

### Codigos de erro

| code                  | http | semantica                                                       |
|-----------------------|------|------------------------------------------------------------------|
| `invalid_phone`       | 400  | nao bate com `^55\d{10,11}$` apos normalizacao                   |
| `invalid_token`       | 400  | token nao bate com nenhum hash                                   |
| `token_expired`       | 410  | hash existe mas `expires_at < now()`                             |
| `already_used`        | 409  | sessao ja em status active/revoked                               |
| `rate_limited`        | 429  | mais de 3 request-link / hora pra mesmo phone (ou 10/h por IP)   |
| `unauthorized`        | 401  | cookie ausente, expirado, revogado                               |
| `not_responsavel`     | 403  | rota familiar e o user logado nao e responsavel                  |
| `not_owner`           | 403  | guardian tentando editar dependente de outro guardian            |
| `phone_already_in_use`| 409  | phone ja cadastrado em users                                     |
| `validation_error`    | 400  | erro de schema (mensagem descreve campo)                         |

Resposta exemplo:

```json
{ "error": { "code": "rate_limited", "message": "Tente novamente em 32 minutos." } }
```

---

## 5. Fluxo do magic link

```
[browser /login] --POST /auth/request-link {phone}-->  [bot api]
                                                        |
                                                        | 1. normaliza phone (so digitos, prefixa 55)
                                                        | 2. valida regex
                                                        | 3. consulta web_login_attempts:
                                                        |    SELECT count(*) WHERE phone=? AND created_at > now-1h
                                                        |    se >= 3 → 429 rate_limited
                                                        | 4. INSERT web_login_attempts
                                                        | 5. db.GetUserByPhone(phone)
                                                        |      not found?  → ainda retorna 200 ok=true (privacy)
                                                        |                    e nao envia mensagem.
                                                        | 6. token = randomBytes(32).toHex()
                                                        | 7. hash   = sha256(token)
                                                        | 8. INSERT web_sessions(user_id, token_hash,
                                                        |     status='pending',
                                                        |     expires_at = now()+15min,
                                                        |     ip, user_agent)
                                                        | 9. url = WEB_BASE_URL + "/auth/verify?token=" + token
                                                        |10. handler.SendTextToPhone(phone,
                                                        |     "Aqui esta seu link de acesso ao painel,
                                                        |      valido por 15 minutos: "+url)
                                                        |11. responde {ok:true}
                                                        v
[browser]  <----------------- 200 {ok:true}  ------------+

usuario abre WhatsApp, ve mensagem, clica no link
                |
                v
[browser /auth/verify?token=XYZ]  --POST /auth/verify {token}-->  [bot api]
                                                                    |
                                                                    | 1. hash = sha256(token)
                                                                    | 2. SELECT * FROM web_sessions
                                                                    |     WHERE token_hash=?
                                                                    |     AND status='pending'
                                                                    |     AND expires_at > now()
                                                                    |   nao achou? → 400/410/409
                                                                    | 3. UPDATE web_sessions SET
                                                                    |     status='active',
                                                                    |     activated_at=now(),
                                                                    |     last_used_at=now(),
                                                                    |     expires_at=now()+30d
                                                                    | 4. SELECT user
                                                                    | 5. Set-Cookie assistente_session=<token>;
                                                                    |     HttpOnly; Secure; SameSite=Strict;
                                                                    |     Max-Age=30d; Path=/
                                                                    | 6. retorna {user: User}
                                                                    v
[browser]  <-- 200 + cookie -- redireciona /dashboard

A partir daqui toda request mutativa pro /api/v1/* manda cookie automaticamente
(credentials: 'include'). Middleware RequireAuth pega o token, faz sha256, le
web_sessions, valida status=active e expires_at>now(), atualiza last_used_at,
injeta o User no context.
```

### Por que SendTextToPhone e nao um endpoint web novo

O bot ja tem o whatsmeow conectado e autenticado. Mandar a mensagem de dentro
do mesmo processo Go evita expor `/api/v1/internal/send-whatsapp` e cabe na
arquitetura atual. O shape do envio:

```go
// dentro de auth_handlers.go (handler do request-link)
msg := fmt.Sprintf(
    "Oi %s, aqui esta seu link de acesso ao painel — vale por 15 minutos: %s\n\n"+
    "Se nao foi voce, pode ignorar.",
    user.Name, magicURL)
if err := s.bot.SendTextToPhone(ctx, user.PhoneNumber, msg); err != nil {
    log.Printf("magic-link send failed: %v", err)
    // ainda retorna 200, evitando enumeracao
}
```

`Handler.SendTextToPhone` ja existe (usado pelo scheduler). O `api.Server`
recebe uma referencia `*Handler` no `New(...)`.

---

## 6. Componentes brasileiros (codigo TypeScript real)

### `web/lib/masks.ts`

```ts
export const onlyDigits = (s: string): string => s.replace(/\D+/g, "");

/**
 * Mascara progressiva pra telefone brasileiro.
 * Aceita 10 digitos (fixo) ou 11 digitos (celular).
 * Persistencia DEVE usar onlyDigits() — esta funcao serve apenas pra display.
 *
 *   "11"           -> "(11"
 *   "1199"         -> "(11) 99"
 *   "11999998888"  -> "(11) 99999-8888"
 *   "1133334444"   -> "(11) 3333-4444"
 */
export function maskPhone(input: string): string {
  const d = onlyDigits(input).slice(0, 11);
  if (d.length === 0) return "";
  if (d.length <= 2) return `(${d}`;
  if (d.length <= 6) return `(${d.slice(0, 2)}) ${d.slice(2)}`;
  if (d.length <= 10) return `(${d.slice(0, 2)}) ${d.slice(2, 6)}-${d.slice(6)}`;
  return `(${d.slice(0, 2)}) ${d.slice(2, 7)}-${d.slice(7)}`;
}

/**
 * Mascara de CEP: XXXXX-XXX. Persistir sem mascara.
 *
 *   "12345"     -> "12345"
 *   "12345678"  -> "12345-678"
 */
export function maskCep(input: string): string {
  const d = onlyDigits(input).slice(0, 8);
  if (d.length <= 5) return d;
  return `${d.slice(0, 5)}-${d.slice(5)}`;
}

/**
 * Mascara de CPF: XXX.XXX.XXX-XX. Persistir sem mascara.
 */
export function maskCpf(input: string): string {
  const d = onlyDigits(input).slice(0, 11);
  if (d.length <= 3) return d;
  if (d.length <= 6) return `${d.slice(0, 3)}.${d.slice(3)}`;
  if (d.length <= 9) return `${d.slice(0, 3)}.${d.slice(3, 6)}.${d.slice(6)}`;
  return `${d.slice(0, 3)}.${d.slice(3, 6)}.${d.slice(6, 9)}-${d.slice(9)}`;
}

/**
 * Normaliza para o formato 55DDDNUMERO (12 ou 13 digitos) que o whatsmeow usa.
 * Retorna null se invalido.
 */
export function normalizePhoneE164BR(input: string): string | null {
  const d = onlyDigits(input);
  if (d.length === 11 || d.length === 10) return `55${d}`;
  if ((d.length === 13 || d.length === 12) && d.startsWith("55")) return d;
  return null;
}

export function isValidPhoneBR(input: string): boolean {
  return normalizePhoneE164BR(input) !== null;
}

export function isValidCepBR(input: string): boolean {
  return onlyDigits(input).length === 8;
}
```

### `web/lib/viacep.ts`

```ts
export interface CepLookupResult {
  cep: string;             // 8 digitos
  logradouro: string;
  bairro: string;
  cidade: string;
  uf: string;
}

export async function parseCepLookup(
  cep: string,
  signal?: AbortSignal,
): Promise<CepLookupResult | null> {
  const digits = cep.replace(/\D+/g, "");
  if (digits.length !== 8) return null;
  const res = await fetch(`https://viacep.com.br/ws/${digits}/json/`, { signal });
  if (!res.ok) return null;
  const data: {
    cep?: string;
    logradouro?: string;
    bairro?: string;
    localidade?: string;
    uf?: string;
    erro?: boolean;
  } = await res.json();
  if (data.erro) return null;
  return {
    cep: digits,
    logradouro: data.logradouro ?? "",
    bairro: data.bairro ?? "",
    cidade: data.localidade ?? "",
    uf: data.uf ?? "",
  };
}
```

### `web/components/ui/PhoneInput.tsx`

```tsx
"use client";

import * as React from "react";
import { Input } from "@/components/ui/input";
import { isValidPhoneBR, maskPhone, onlyDigits } from "@/lib/masks";

export interface PhoneInputProps
  extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "value" | "onChange"> {
  value: string;                         // sempre digitos puros (sem mascara)
  onChange: (digits: string) => void;    // emite digitos puros
  invalidMessage?: string;
}

export const PhoneInput = React.forwardRef<HTMLInputElement, PhoneInputProps>(
  function PhoneInput(
    { value, onChange, invalidMessage, onBlur, ...rest },
    ref,
  ) {
    const [touched, setTouched] = React.useState(false);
    const display = maskPhone(value);
    const showError = touched && value.length > 0 && !isValidPhoneBR(value);

    return (
      <div className="flex flex-col gap-1">
        <Input
          ref={ref}
          type="tel"
          inputMode="numeric"
          autoComplete="tel-national"
          placeholder="(11) 99999-8888"
          value={display}
          onChange={(e) => onChange(onlyDigits(e.target.value).slice(0, 11))}
          onBlur={(e) => {
            setTouched(true);
            onBlur?.(e);
          }}
          aria-invalid={showError || undefined}
          {...rest}
        />
        {showError && (
          <span className="text-sm text-red-600">
            {invalidMessage ?? "Telefone invalido. Use DDD + numero."}
          </span>
        )}
      </div>
    );
  },
);
```

### `web/components/ui/CepInput.tsx`

```tsx
"use client";

import * as React from "react";
import { Input } from "@/components/ui/input";
import { isValidCepBR, maskCep, onlyDigits } from "@/lib/masks";
import { parseCepLookup, type CepLookupResult } from "@/lib/viacep";

export interface CepInputProps
  extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "value" | "onChange"> {
  value: string;
  onChange: (digits: string) => void;
  /**
   * Disparado quando o ViaCEP devolve um endereco valido. Implementacoes
   * tipicas devem preencher logradouro/bairro/cidade/uf APENAS se os campos
   * destino estiverem vazios (ver regra global do user).
   */
  onLookup?: (result: CepLookupResult) => void;
  invalidMessage?: string;
}

export const CepInput = React.forwardRef<HTMLInputElement, CepInputProps>(
  function CepInput({ value, onChange, onLookup, invalidMessage, ...rest }, ref) {
    const [loading, setLoading] = React.useState(false);
    const [lookupError, setLookupError] = React.useState<string | null>(null);
    const lastLookedUp = React.useRef<string>("");

    React.useEffect(() => {
      if (!isValidCepBR(value)) return;
      if (lastLookedUp.current === value) return;
      const ctrl = new AbortController();
      lastLookedUp.current = value;
      setLoading(true);
      setLookupError(null);
      parseCepLookup(value, ctrl.signal)
        .then((res) => {
          if (!res) {
            setLookupError("CEP nao encontrado.");
            return;
          }
          onLookup?.(res);
        })
        .catch((e) => {
          if (e?.name !== "AbortError") {
            setLookupError("Falha ao buscar CEP. Tente de novo.");
          }
        })
        .finally(() => setLoading(false));
      return () => ctrl.abort();
    }, [value, onLookup]);

    const display = maskCep(value);
    const showInvalid = value.length > 0 && value.length < 8;

    return (
      <div className="flex flex-col gap-1">
        <Input
          ref={ref}
          inputMode="numeric"
          autoComplete="postal-code"
          placeholder="00000-000"
          value={display}
          onChange={(e) => onChange(onlyDigits(e.target.value).slice(0, 8))}
          {...rest}
        />
        {loading && <span className="text-sm text-gray-500">Buscando endereco...</span>}
        {lookupError && <span className="text-sm text-red-600">{lookupError}</span>}
        {showInvalid && !loading && !lookupError && (
          <span className="text-sm text-gray-500">
            {invalidMessage ?? "Continue digitando — 8 numeros."}
          </span>
        )}
      </div>
    );
  },
);
```

Uso tipico no formulario (regra: so preenche se vazio):

```tsx
<CepInput
  value={cep}
  onChange={setCep}
  onLookup={(r) => {
    setLogradouro((cur) => cur || r.logradouro);
    setBairro((cur) => cur || r.bairro);
    setCidade((cur) => cur || r.cidade);
    setUf((cur) => cur || r.uf);
  }}
/>
```

### `web/components/ui/TimezoneSelect.tsx`

```tsx
"use client";

import * as React from "react";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { BR_TIMEZONES } from "@/lib/timezones";

export interface TimezoneSelectProps {
  value: string;
  onChange: (tz: string) => void;
}

export function TimezoneSelect({ value, onChange }: TimezoneSelectProps) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger>
        <SelectValue placeholder="Escolha um fuso" />
      </SelectTrigger>
      <SelectContent>
        {BR_TIMEZONES.map((tz) => (
          <SelectItem key={tz.id} value={tz.id}>
            {tz.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
```

```ts
// web/lib/timezones.ts
export const BR_TIMEZONES = [
  { id: "America/Sao_Paulo", label: "Brasilia (GMT-3) — SP, RJ, MG, ..." },
  { id: "America/Bahia", label: "Bahia (GMT-3)" },
  { id: "America/Fortaleza", label: "Fortaleza (GMT-3)" },
  { id: "America/Recife", label: "Recife (GMT-3)" },
  { id: "America/Belem", label: "Belem (GMT-3)" },
  { id: "America/Manaus", label: "Manaus (GMT-4)" },
  { id: "America/Cuiaba", label: "Cuiaba (GMT-4)" },
  { id: "America/Campo_Grande", label: "Campo Grande (GMT-4)" },
  { id: "America/Porto_Velho", label: "Porto Velho (GMT-4)" },
  { id: "America/Boa_Vista", label: "Boa Vista (GMT-4)" },
  { id: "America/Rio_Branco", label: "Rio Branco (GMT-5)" },
  { id: "America/Eirunepe", label: "Eirunepe (GMT-5)" },
  { id: "America/Noronha", label: "Fernando de Noronha (GMT-2)" },
] as const;

export type BRTimezoneId = (typeof BR_TIMEZONES)[number]["id"];
```

---

## 7. Auth middleware Go

### `bot/api/sessions.go`

```go
package api

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	magicLinkTTL  = 15 * time.Minute
	sessionTTL    = 30 * 24 * time.Hour
	tokenByteLen  = 32
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrSessionExpired  = errors.New("session expired")
	ErrSessionUsed     = errors.New("session already used or revoked")
)

type WebSession struct {
	ID          int64
	UserID      int64
	Status      string
	ExpiresAt   time.Time
	CreatedAt   time.Time
	ActivatedAt sql.NullTime
	LastUsedAt  sql.NullTime
	IP          string
	UserAgent   string
}

func generateToken() (plain, hash string, err error) {
	buf := make([]byte, tokenByteLen)
	if _, err = rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("rand: %w", err)
	}
	plain = hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plain))
	hash = hex.EncodeToString(sum[:])
	return plain, hash, nil
}

func hashToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

func (s *Server) createPendingSession(userID int64, ip, ua string) (plain string, err error) {
	plain, hash, err := generateToken()
	if err != nil {
		return "", err
	}
	_, err = s.db.Exec(`
		INSERT INTO web_sessions (user_id, token_hash, status, expires_at, ip, user_agent)
		VALUES (?, ?, 'pending', ?, ?, ?)`,
		userID, hash, time.Now().UTC().Add(magicLinkTTL), ip, ua)
	if err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}
	return plain, nil
}

func (s *Server) activateSession(plain string) (*WebSession, error) {
	hash := hashToken(plain)
	row := s.db.QueryRow(`
		SELECT id, user_id, status, expires_at, created_at, activated_at, last_used_at, ip, user_agent
		FROM web_sessions WHERE token_hash = ?`, hash)
	var ws WebSession
	err := row.Scan(&ws.ID, &ws.UserID, &ws.Status, &ws.ExpiresAt, &ws.CreatedAt,
		&ws.ActivatedAt, &ws.LastUsedAt, &ws.IP, &ws.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, err
	}
	if ws.Status != "pending" {
		return nil, ErrSessionUsed
	}
	if time.Now().UTC().After(ws.ExpiresAt) {
		_, _ = s.db.Exec(`UPDATE web_sessions SET status='expired' WHERE id=?`, ws.ID)
		return nil, ErrSessionExpired
	}
	now := time.Now().UTC()
	exp := now.Add(sessionTTL)
	_, err = s.db.Exec(`
		UPDATE web_sessions SET status='active', activated_at=?, last_used_at=?, expires_at=?
		WHERE id=?`, now, now, exp, ws.ID)
	if err != nil {
		return nil, err
	}
	ws.Status = "active"
	ws.ActivatedAt = sql.NullTime{Time: now, Valid: true}
	ws.LastUsedAt = sql.NullTime{Time: now, Valid: true}
	ws.ExpiresAt = exp
	return &ws, nil
}

func (s *Server) touchSession(plain string) (*WebSession, error) {
	hash := hashToken(plain)
	row := s.db.QueryRow(`
		SELECT id, user_id, status, expires_at FROM web_sessions WHERE token_hash = ?`, hash)
	var ws WebSession
	if err := row.Scan(&ws.ID, &ws.UserID, &ws.Status, &ws.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	if ws.Status != "active" {
		return nil, ErrSessionUsed
	}
	now := time.Now().UTC()
	if now.After(ws.ExpiresAt) {
		_, _ = s.db.Exec(`UPDATE web_sessions SET status='expired' WHERE id=?`, ws.ID)
		return nil, ErrSessionExpired
	}
	exp := now.Add(sessionTTL)
	if _, err := s.db.Exec(
		`UPDATE web_sessions SET last_used_at=?, expires_at=? WHERE id=?`,
		now, exp, ws.ID); err != nil {
		return nil, err
	}
	ws.LastUsedAt = sql.NullTime{Time: now, Valid: true}
	ws.ExpiresAt = exp
	return &ws, nil
}

func (s *Server) revokeSession(plain string) error {
	hash := hashToken(plain)
	_, err := s.db.Exec(
		`UPDATE web_sessions SET status='revoked', revoked_at=CURRENT_TIMESTAMP
		 WHERE token_hash=? AND status='active'`, hash)
	return err
}
```

### `bot/api/middleware.go`

```go
package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
)

const cookieName = "assistente_session"

type ctxKey int

const ctxUserKey ctxKey = 1

func userFromContext(ctx context.Context) (*User, bool) {
	u, ok := ctx.Value(ctxUserKey).(*User)
	return u, ok
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && origin == s.cfg.WebOrigin {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PATCH,DELETE,OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.Header().Set("Access-Control-Max-Age", "86400")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireOrigin protege endpoints mutativos contra CSRF. Em conjunto com
// SameSite=strict, exige header Origin presente e batendo com o web origin
// configurado.
func (s *Server) requireOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" || origin != s.cfg.WebOrigin {
			writeError(w, http.StatusForbidden, "forbidden_origin",
				"Origem nao autorizada para esta operacao.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || strings.TrimSpace(c.Value) == "" {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Faca login.")
			return
		}
		ws, err := s.touchSession(c.Value)
		if err != nil {
			switch {
			case errors.Is(err, ErrSessionNotFound),
				errors.Is(err, ErrSessionExpired),
				errors.Is(err, ErrSessionUsed):
				clearSessionCookie(w, s.cfg.SecureCookies)
				writeError(w, http.StatusUnauthorized, "unauthorized", "Sessao invalida.")
				return
			default:
				log.Printf("touchSession error: %v", err)
				writeError(w, http.StatusInternalServerError, "internal", "Erro interno.")
				return
			}
		}
		user, err := s.db.GetUserByID(ws.UserID)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Sessao invalida.")
			return
		}
		ctx := context.WithValue(r.Context(), ctxUserKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) requireResponsavel(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := userFromContext(r.Context())
		if !ok || u.UserType != "responsavel" {
			writeError(w, http.StatusForbidden, "not_responsavel",
				"Apenas responsaveis podem acessar esta secao.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setSessionCookie(w http.ResponseWriter, token string, secure bool) {
	c := &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
	http.SetCookie(w, c)
}

func clearSessionCookie(w http.ResponseWriter, secure bool) {
	c := &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
	http.SetCookie(w, c)
}
```

### `bot/api/server.go`

```go
package api

import (
	"net/http"
)

type Config struct {
	WebOrigin       string  // ex "https://assistente.itacitrus.com.br"
	WebBaseURL      string  // mesmo origin sem trailing slash; usado pra montar magic link
	SecureCookies   bool    // true em prod
	RateLimitPerHr  int     // default 3
}

type Server struct {
	cfg    Config
	db     *DB
	bot    BotMessenger // interface com SendTextToPhone(ctx, phone, msg)
}

func New(cfg Config, db *DB, bot BotMessenger) *Server {
	return &Server{cfg: cfg, db: db, bot: bot}
}

func (s *Server) Mount(mux *http.ServeMux) {
	mux.Handle("/api/v1/auth/request-link",
		s.cors(s.requireOrigin(http.HandlerFunc(s.handleRequestLink))))
	mux.Handle("/api/v1/auth/verify",
		s.cors(s.requireOrigin(http.HandlerFunc(s.handleVerify))))
	mux.Handle("/api/v1/auth/logout",
		s.cors(s.requireOrigin(s.requireAuth(http.HandlerFunc(s.handleLogout)))))
	mux.Handle("/api/v1/me",
		s.cors(s.requireAuth(http.HandlerFunc(s.handleMe))))
	mux.Handle("/api/v1/users/me",
		s.cors(s.requireOrigin(s.requireAuth(http.HandlerFunc(s.handlePatchMe)))))
	mux.Handle("/api/v1/family/dependents",
		s.cors(s.requireOrigin(s.requireAuth(s.requireResponsavel(
			http.HandlerFunc(s.handleDependents))))))
	mux.HandleFunc("/api/v1/family/dependents/", // PATCH /{id}
		s.requireOriginFunc(s.requireAuthFunc(s.requireResponsavelFunc(s.handleDependentByID))))
	mux.HandleFunc("/api/v1/family/links/",
		s.requireOriginFunc(s.requireAuthFunc(s.requireResponsavelFunc(s.handleLinkNotify))))
}
```

(Os helpers `requireAuthFunc`, `requireOriginFunc`, etc, sao versoes
`http.HandlerFunc` dos middlewares acima — omitidos por brevidade mas seguem
o mesmo padrao.)

### Hook em `bot/main.go`

```go
// dentro de startOAuthServer, antes do ListenAndServe:
mux := http.DefaultServeMux

apiSrv := api.New(api.Config{
    WebOrigin:      cfg.WebOrigin,
    WebBaseURL:     cfg.WebBaseURL,
    SecureCookies:  cfg.Env == "production",
    RateLimitPerHr: 3,
}, dbAPIAdapter{db: db}, handler /* tem SendTextToPhone */)
apiSrv.Mount(mux)
```

`Config` ganha campos `WebOrigin`, `WebBaseURL`, `Env`. `dbAPIAdapter` adapta
`*DB` pro contrato `api.DB` (apenas re-exporta os metodos relevantes —
`GetUserByID`, `GetUserByPhone`, `CreateDependent`, `ListDependentsOf`,
`UpdateUserPreferences`, `UpdateFamilyLinkNotify`, etc). Mantem o pacote `api`
desacoplado do `main`.

---

## 8. Telas (wireframes ASCII)

### `/` — Landing

```
+------------------------------------------------------------------+
| Charles Lurch.                                          [ Login ]|
+------------------------------------------------------------------+
|                                                                  |
|   Sua agenda em boas maos.                                       |
|   O Charles cuida do calendario da sua familia                   |
|   pelo WhatsApp, sem app pra instalar.                           |
|                                                                  |
|   [ Criar conta ]   [ Saiba mais ]                               |
|                                                                  |
|   ----------------------------------------------------------     |
|   Como funciona:                                                 |
|     1. Voce cria uma conta com seu numero.                       |
|     2. O Charles entra em contato pelo WhatsApp.                 |
|     3. Pronto — fala com ele em portugues normal.                |
|                                                                  |
+------------------------------------------------------------------+
```

### `/signup`

```
+--------------------------- Crie sua conta -----------------------+
|                                                                  |
|  Nome completo                                                   |
|  [ Maria Silva__________________________________ ]               |
|                                                                  |
|  Telefone (com DDD)                                              |
|  [ (11) 99999-8888 ]                                             |
|                                                                  |
|  Voce vai usar o Charles pra:                                    |
|  ( ) Cuidar da minha agenda                                      |
|  ( ) Cuidar de alguem (responsavel por idoso, filho, etc.)       |
|                                                                  |
|  [ Criar conta ]                                                 |
|                                                                  |
|  Ja tem conta? Fazer login                                       |
+------------------------------------------------------------------+
```

Submit: `POST /api/v1/auth/request-link` — fluxo idem login (cria user na hora
se nao existe; backend faz upsert quando vem de `/signup`, identificado por
flag opcional no body `{ phone, name?, user_type? }`).

### `/login`

```
+--------------------------- Entrar -------------------------------+
|                                                                  |
|  Vamos te mandar um link de acesso pelo WhatsApp.                |
|                                                                  |
|  Telefone                                                        |
|  [ (11) 99999-8888 ]                                             |
|                                                                  |
|  [ Enviar link ]                                                 |
|                                                                  |
|  --- depois do submit ---                                        |
|  Pronto — se este numero esta cadastrado, voce vai receber       |
|  um link no WhatsApp em alguns segundos.                         |
+------------------------------------------------------------------+
```

### `/auth/verify?token=...`

```
+------------------------------------------------------------------+
|              Validando seu link...                               |
|              [spinner]                                           |
+------------------------------------------------------------------+
```

Client component: na montagem chama `POST /api/v1/auth/verify { token }` com
`credentials: 'include'`. Sucesso → `router.replace('/dashboard')`. Erro →
mostra mensagem ("link expirado, peca outro" / "link ja usado").

### `/dashboard`

```
+ Charles Lurch ---------------------------------------- [ Sair ] +
|                                                                  |
|  Ola, Maria.                                                     |
|                                                                  |
|  +------------------------------------------------------------+  |
|  | Sua agenda                                                 |  |
|  | Conectada ao Google Calendar (maria@gmail.com)             |  |
|  | [ Reconectar ]                                             |  |
|  +------------------------------------------------------------+  |
|                                                                  |
|  Pessoas que voce cuida:                                         |
|  +-----------------+ +-----------------+                         |
|  | Vovo Joana      | |  + Adicionar    |                         |
|  | (filha)         | |    pessoa       |                         |
|  | [ Ver detalhes ]| |                 |                         |
|  +-----------------+ +-----------------+                         |
|                                                                  |
|  Sidebar: [ Inicio ] [ Preferencias ] [ Familia ]                |
+------------------------------------------------------------------+
```

(Botao "Ver detalhes" leva pra `/dashboard/family/[id]/preferences` nesta fase
— a tela rica de status fica pra Fase 5.)

### `/dashboard/preferences`

```
+ Preferencias ----------------------------------------------------+
|                                                                  |
|  Fuso horario                                                    |
|  [ Brasilia (GMT-3) — SP, RJ, MG, ...           v ]              |
|                                                                  |
|  Resumo diario                                                   |
|  [x] Receber resumo diario as [ 07:00 ]                          |
|                                                                  |
|  Lembrete antes do evento                                        |
|  ( ) 15 minutos  ( ) 30 minutos  (x) 1 hora  ( ) 2 horas         |
|                                                                  |
|  Tempo de auto-confirmacao                                       |
|  ( ) 30 min  ( ) 1h  (x) 2h  ( ) 4h  ( ) Nunca                   |
|                                                                  |
|  [ Salvar ]                                                      |
+------------------------------------------------------------------+
```

### `/dashboard/family/new`

```
+ Adicionar pessoa que voce cuida ---------------------------------+
|                                                                  |
|  Nome                                                            |
|  [ ____________________________________________ ]                |
|                                                                  |
|  Telefone (com DDD)                                              |
|  [ (11) 99999-8888 ]                                             |
|  > Esta pessoa precisa ter WhatsApp neste numero.                |
|                                                                  |
|  Qual e a relacao?                                               |
|  [ filha (de) ____ ] (livre, ex: filha, filho, esposa, mae)      |
|                                                                  |
|  Fuso horario                                                    |
|  [ Brasilia (GMT-3) — SP, RJ, MG, ...           v ]              |
|                                                                  |
|  [ Cadastrar ]                                                   |
+------------------------------------------------------------------+
```

### `/dashboard/family/[id]/preferences`

```
+ Vovo Joana — preferencias de cuidado ----------------------------+
|                                                                  |
|  Sobre a Vovo                                                    |
|  Nome:     [ Joana Souza ____________________ ]                  |
|  Fuso:     [ Brasilia (GMT-3) v ]                                |
|  Resumo diario as [ 09:00 ]                                      |
|  Lembrete antes do evento: ( )15m ( )30m (x)1h ( )2h             |
|                                                                  |
|  ----------------------------------------------------------      |
|  Avise voce (responsavel) quando:                                |
|  [x] ela perder um medicamento                                   |
|  [x] ela perder um evento                                        |
|  [ ] ela mudar a agenda                                          |
|  [x] o celular dela ficar com bateria baixa                      |
|                                                                  |
|  Resumo diario por WhatsApp para voce:                           |
|  [x] enviar as [ 21:00 ]                                         |
|                                                                  |
|  [ Salvar ]                                                      |
+------------------------------------------------------------------+
```

---

## 9. Seguranca — checklist

### Cookies

- [x] `HttpOnly` (impede leitura por JS).
- [x] `Secure` em producao (env-driven).
- [x] `SameSite=Strict` (impede CSRF cross-site).
- [x] `Path=/`.
- [x] Sem dominio explicito (host-only) → cookie nao vaza pra subdominios.
- [x] `Max-Age=30d`, sliding window.

### CORS

- [x] `Access-Control-Allow-Origin` apenas `WEB_ORIGIN` exato (string match,
      sem wildcard).
- [x] `Access-Control-Allow-Credentials: true`.
- [x] `Vary: Origin` pra cache.
- [x] Preflight OPTIONS retorna 204.
- [x] Endpoints de admin/oauth legacy ficam fora do CORS web.

### CSRF

- [x] SameSite=Strict ja bloqueia browsers modernos.
- [x] Belt-and-suspenders: middleware `requireOrigin` em todo POST/PATCH/DELETE
      checa header `Origin` contra `WEB_ORIGIN`.
- [x] Sem support de submit por form-encoded — content-type tem que ser JSON.

### Rate limit

- [x] `POST /auth/request-link`: 3/hora por phone, 10/hora por IP, persistido
      em `web_login_attempts`.
- [x] Job de housekeeping: `DELETE FROM web_login_attempts WHERE created_at <
      now() - 7d` (rodar dentro do scheduler ja existente, 1x/dia).

### Tokens

- [x] 32 bytes random via `crypto/rand` (256 bits).
- [x] Plaintext exposto **so** no link enviado pelo WhatsApp e no cookie.
- [x] `web_sessions.token_hash` armazena `sha256(plain)` — comprometer o DB
      nao revela tokens validos.
- [x] `UNIQUE (token_hash)` previne colisao.

### TTL

- [x] Magic link: 15 min hard limit (recusado mesmo se nao foi usado).
- [x] Sessao ativa: 30d sliding (renova a cada request autenticado).
- [x] Sweep: scheduler roda 1x/h `UPDATE web_sessions SET status='expired'
      WHERE status IN ('pending','active') AND expires_at < now()`.

### HTTPS

- [x] Bot Go atras de Caddy/Nginx ja com TLS (infra atual).
- [x] Redirect 80 → 443 ja no Caddy.
- [x] HSTS no Caddy: `Strict-Transport-Security: max-age=31536000; includeSubDomains`.
- [x] Em dev, `SECURE_COOKIES=false` permite cookies em http://localhost.

### Sanitizacao

- [x] Validacao de body via decoder com `DisallowUnknownFields`.
- [x] Phone normalizado e validado por regex `^55\d{10,11}$`.
- [x] Strings (name, relation) trim + limite de tamanho + reject de `\x00`.
- [x] Timezone tem que estar em allowlist (constante).
- [x] No SQLite, todas as queries sao parametrizadas (driver `modernc.org/sqlite`).

### Privacidade

- [x] `request-link` retorna 200 mesmo se phone nao existe (evita enumeracao).
- [x] `verify` distingue `invalid`, `expired`, `already_used` — ok porque nao
      existe enumeracao se voce ja tem o token. Mensagens em PT-BR sao para o
      usuario final.
- [x] Logs nao gravam token plaintext nem cookie value.

### Auditoria

- [x] Logar `action_log` com action `web_login` em activate de sessao
      (ja existe a tabela; reusar `db.LogAction`).

---

## 10. Plano de implementacao (PRs)

Cada PR e mergeavel sozinho, com testes, sem quebrar nada do bot atual.

1. **PR-A — schema web_sessions + web_login_attempts**
   - DDL em `db.go` (additive migration).
   - Funcoes `CreateWebSession`, `GetWebSessionByHash`, `UpdateWebSessionStatus`,
     `RecordLoginAttempt`, `CountLoginAttemptsSince`.
   - Testes unitarios em `db_test.go` cobrindo lifecycle e expiry.
   - Sem rota nova; soh DB.

2. **PR-B — pacote `bot/api/` esqueleto + middleware**
   - `Server`, `New`, `Mount` (com mux passado de fora).
   - `cors`, `requireOrigin`, `requireAuth`, `requireResponsavel`.
   - `errors.go` (writeError / writeJSON).
   - Endpoint `GET /api/v1/me` apenas (placeholder, util pra fumar a auth).
   - Cobertura: testa middleware mockando DB/handler.

3. **PR-C — magic link end-to-end**
   - `auth_handlers.go`: `handleRequestLink`, `handleVerify`, `handleLogout`.
   - `magic_link.go`: geracao/hash de token, monta URL.
   - Integracao com `Handler.SendTextToPhone`.
   - Rate limit usando `web_login_attempts`.
   - Env `WEB_ORIGIN`, `WEB_BASE_URL`, `SECURE_COOKIES` em `Config`.
   - Testes de integracao (httptest + sqlite em memoria).

4. **PR-D — bootstrap do `/web` Next.js**
   - `npx create-next-app@latest web --typescript --tailwind --app --eslint`.
   - `pnpm dlx shadcn@latest init` + adiciona button/input/label/card/select.
   - `lib/masks.ts`, `lib/viacep.ts`, `lib/api.ts`, `lib/timezones.ts`.
   - `components/ui/PhoneInput.tsx`, `CepInput.tsx`, `TimezoneSelect.tsx`.
   - Testes Vitest pra `masks.ts` (regressao da mascara).
   - `vercel.json` ou `.vercel/project.json` ao linkar.

5. **PR-E — paginas publicas (landing + signup + login + verify)**
   - `app/page.tsx`, `app/signup/page.tsx`, `app/login/page.tsx`,
     `app/auth/verify/page.tsx`.
   - `components/forms/SignupForm.tsx`, `LoginForm.tsx`.
   - `middleware.ts` redireciona `/dashboard/*` sem cookie pra `/login`.
   - Backend: `request-link` aceita opcionalmente `{name, user_type}` no
     mesmo endpoint pra fluxo de signup (cria user antes de mandar o link).

6. **PR-F — dashboard e preferencias do proprio user**
   - `app/dashboard/layout.tsx`, `app/dashboard/page.tsx`,
     `app/dashboard/preferences/page.tsx`.
   - `components/forms/PreferencesForm.tsx`.
   - Backend: `PATCH /api/v1/users/me`.
   - Validacoes: timezone allowlist, time format, reminder_before enum.

7. **PR-G — gestao familiar (dependentes)**
   - `app/dashboard/family/new/page.tsx`,
     `app/dashboard/family/[id]/preferences/page.tsx`.
   - `components/forms/DependentForm.tsx`, `NotifyPreferencesForm.tsx`.
   - Backend: `POST/GET /family/dependents`,
     `PATCH /family/dependents/{id}`,
     `PATCH /family/links/{id}/notify`.
   - Regra: somente guardian dono do link pode editar (verifica
     `family_links.guardian_id == ctxUser.id`).

8. **PR-H — housekeeping + observabilidade**
   - Job 1x/h no scheduler: expira sessoes vencidas, limpa
     `web_login_attempts` antigos.
   - Logs estruturados (mesmo padrao da Fase robustez): `web_login_requested`,
     `web_login_succeeded`, `web_session_revoked`, `web_dependent_created`.
   - Metricas basicas: count de `request-link` por dia, count de `verify`
     bem-sucedidos.

9. **PR-I — deploy**
   - Vercel: linka `/web/`, define env `NEXT_PUBLIC_API_BASE_URL`.
   - Bot: terraform adiciona env `WEB_ORIGIN`, `WEB_BASE_URL`,
     `SECURE_COOKIES=true`.
   - Caddy: bloco pra responder a `assistente.itacitrus.com.br` (web Vercel
     via DNS) e `api.assistente.itacitrus.com.br` (bot Go).
   - QA manual usando o checklist da secao 12.

---

## 11. Riscos

| risco                                                 | impacto                                 | mitigacao                                                                                                     |
|-------------------------------------------------------|------------------------------------------|---------------------------------------------------------------------------------------------------------------|
| **Auth fraca**                                        | conta sequestrada                        | token 256-bit, hash em DB, ttl 15min, sessao httpOnly+secure+SameSite, rate limit                            |
| **DoS no `request-link`**                             | spam WhatsApp, ban por whatsmeow         | rate limit 3/h por phone + 10/h por IP, alerta se `web_login_attempts` por hora > N global                   |
| **Magic link interceptado** (compartilhar print do WA)| qualquer um que veja o print loga       | TTL 15min, single-use (status=active impede re-uso), exibir aviso no `/login` "nao compartilhe este link"     |
| **Phone enumeration**                                 | atacante descobre quem e cliente        | resposta 200 opaca em request-link mesmo sem user, sem disclosure no body                                     |
| **Deploy separado complica env**                      | quebra de origin/cors, cookie nao seta   | cookbook de env no `00-overview.md`, healthcheck `/api/v1/me` sem cookie devolve 401 esperado                |
| **CORS misconfig em prod**                            | app web nao consegue logar              | smoke test pos-deploy: curl `OPTIONS /api/v1/auth/verify -H "Origin: $WEB_ORIGIN"` deve devolver 204 + headers|
| **Race entre `verify` concorrentes** (mesmo token)    | dois clicks simultaneos um vence       | UPDATE com WHERE status='pending', checa rows affected; o segundo cai em `already_used`                       |
| **whatsmeow desconectado quando manda link**          | usuario nunca recebe                    | log de erro, retry-once async, healthcheck do whatsmeow (existe), fallback: avisar usuario "tente novamente em alguns minutos" se erro de envio sincrono |
| **Cookie leakage cross-subdomain**                    | sessao escapa pra outro app              | cookie host-only (sem Domain), Path=/, e nao usar wildcard CORS                                                |
| **SQLite e WAL em alta concorrencia de login**        | locks                                   | busy_timeout=5000 ja configurado, INSERT em web_sessions e simples, sem joins                                  |
| **`/auth/verify` por GET (link no preview do WA)**    | preview do WhatsApp consome o token     | endpoint e `POST` (preview do WA so faz HEAD/GET), pagina `/auth/verify` em Next chama POST do client; alternativa: pagina mostra botao "confirmar acesso" se quisermos belt-and-suspenders. **Decisao: ja basta ser POST.** |

---

## 12. Checklist de pronto

### Backend (Go)

- [ ] Migracoes `web_sessions` e `web_login_attempts` rodam idempotente
- [ ] `crypto/rand` gera tokens de 32 bytes
- [ ] `sha256` armazenado em `token_hash`
- [ ] `POST /api/v1/auth/request-link` retorna 200 mesmo sem usuario
- [ ] Rate limit 3/h por phone e 10/h por IP
- [ ] `POST /api/v1/auth/verify` valida hash + status + expiry e seta cookie
- [ ] Cookie `HttpOnly`, `Secure` (em prod), `SameSite=Strict`
- [ ] `requireAuth` middleware injeta user no context
- [ ] `requireResponsavel` middleware bloqueia tipo errado
- [ ] `requireOrigin` middleware bloqueia POST sem header Origin valido
- [ ] CORS responde com `WEB_ORIGIN` exato e `Allow-Credentials: true`
- [ ] Sweep horario de sessoes expiradas no scheduler
- [ ] Logs estruturados: web_login_requested/succeeded/failed
- [ ] Testes: middleware, magic link lifecycle, rate limit, dependents CRUD

### Frontend (`/web`)

- [ ] Next.js 14 App Router + TS strict + Tailwind + shadcn/ui
- [ ] `maskPhone`, `maskCep`, `onlyDigits` cobertos por testes
- [ ] `<PhoneInput>` usado em todos os formularios com telefone
- [ ] `<CepInput>` integra ViaCEP, so preenche campos vazios
- [ ] `<TimezoneSelect>` lista todos os fusos BR
- [ ] Persistencia salva digitos puros (sem mascara) em todos os payloads
- [ ] `lib/api.ts` usa `credentials: "include"` em todas as chamadas
- [ ] `middleware.ts` redireciona `/dashboard/*` sem cookie pra `/login`
- [ ] Pagina `/auth/verify` chama POST do client e trata 410/409 com mensagem
- [ ] Sem menus numerados em copy de mensagem do bot
- [ ] Strings em pt-BR, sem placeholders em ingles
- [ ] Erros de form em PT-BR, exibidos abaixo do campo
- [ ] Lighthouse mobile ≥ 90 nas paginas publicas

### Deploy

- [ ] Vercel linkado em `/web/` com env `NEXT_PUBLIC_API_BASE_URL`
- [ ] Terraform define `WEB_ORIGIN`, `WEB_BASE_URL`, `SECURE_COOKIES=true`
- [ ] DNS aponta `assistente.itacitrus.com.br` pra Vercel
- [ ] DNS aponta `api.assistente.itacitrus.com.br` pro bot
- [ ] Caddy/Nginx faz reverse proxy do `api.*` pro bot:8080
- [ ] HSTS + redirect 80→443 ativos
- [ ] Smoke test pos-deploy: signup → magic link no WhatsApp → verify → /dashboard
- [ ] Smoke test: cadastro de dependente persiste em `users` (tipo idoso) +
      `family_links`
- [ ] Smoke test: edicao de preferencia do dependente atualiza
      `family_links.notify_*`
- [ ] Smoke test: logout limpa cookie e devolve 401 em /me

### Documentacao

- [ ] `web/README.md` com `pnpm dev`, env vars, link do bot local
- [ ] `bot/README.md` com instrucoes de rodar API local + tunnel pro WhatsApp
- [ ] `docs/superpowers/plans/2026-05-09-idosos/03-*.md` (proxima fase) ja
      pode assumir auth pronta

---

## Apendice A — variaveis de ambiente novas

### Bot (`bot/.env`)

```
WEB_ORIGIN=https://assistente.itacitrus.com.br
WEB_BASE_URL=https://assistente.itacitrus.com.br
SECURE_COOKIES=true
ENV=production
```

Em dev:

```
WEB_ORIGIN=http://localhost:3000
WEB_BASE_URL=http://localhost:3000
SECURE_COOKIES=false
ENV=development
```

### Web (`web/.env.local`)

```
NEXT_PUBLIC_API_BASE_URL=http://localhost:8080
```

Em prod (Vercel):

```
NEXT_PUBLIC_API_BASE_URL=https://api.assistente.itacitrus.com.br
```

## Apendice B — exemplo de fetch wrapper

```ts
// web/lib/api.ts
const BASE = process.env.NEXT_PUBLIC_API_BASE_URL!;

export class ApiError extends Error {
  constructor(
    public code: string,
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
  });
  const text = await res.text();
  const body = text ? JSON.parse(text) : null;
  if (!res.ok) {
    const code = body?.error?.code ?? "unknown";
    const message = body?.error?.message ?? `HTTP ${res.status}`;
    throw new ApiError(code, res.status, message);
  }
  return body as T;
}

export const api = {
  get:    <T>(p: string)         => request<T>(p),
  post:   <T>(p: string, b: any) => request<T>(p, { method: "POST",   body: JSON.stringify(b) }),
  patch:  <T>(p: string, b: any) => request<T>(p, { method: "PATCH",  body: JSON.stringify(b) }),
  delete: <T>(p: string)         => request<T>(p, { method: "DELETE" }),
};
```
