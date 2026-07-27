# Pareamento do WhatsApp pela página admin

**Data:** 2026-07-27
**Status:** aprovado, em implementação

## Problema

Ao formatar o celular, o WhatsApp invalida o dispositivo vinculado no servidor. O
whatsmeow recebe o logout e **apaga o device localmente** (`whatsmeow_device` fica
vazia). O processo do bot continua de pé, mas preso em reconexão inútil
(`invalid use of deleted device`) — o Watchdog só reconecta, não repareia.

Hoje o pareamento só acontece **uma vez, no boot**, bloqueando na leitura do QR do
stdout (`for evt := range qrChan`). Isso não serve para um ambiente Docker/systemd
sem TTY, nem para reparear sob demanda.

## Objetivo

Uma página exclusiva do admin (`/dashboard/admin/whatsapp`) que gera **código de 8
dígitos** (pareamento por número) **e** **QR code**, para o operador parear pela web
sem SSH/SSM. Deve funcionar tanto no estado "deslogado" quanto para trocar o número
de um bot saudável.

## Arquitetura

O nó do problema é o ciclo de vida do client whatsmeow: parear sob demanda exige
trocar a identidade do client em **runtime**, com todos os consumidores (handler,
watchdog, scheduler) passando a usar o client novo sem reiniciar o processo.

### 1. `ClientHolder` (novo, pacote `main`)

Indireção atômica sobre `*whatsmeow.Client`:

```go
type ClientHolder struct { mu sync.RWMutex; c *whatsmeow.Client }
func (h *ClientHolder) Get() *whatsmeow.Client
func (h *ClientHolder) Set(c *whatsmeow.Client)
```

`handler.client` e `watchdog.client` deixam de capturar o ponteiro uma vez e passam
a ler `holder.Get()`. Quando o pareamento conclui, `holder.Set(novoClient)` rewira
tudo de uma vez. Scheduler e notifier já vão via `handler.SendTextToPhone`, então
não precisam mudar.

**Invariante:** nenhum consumidor guarda `*whatsmeow.Client` diretamente — sempre
`holder.Get()` no ponto de uso.

### 2. `PairingManager` (novo, pacote `main`)

Dono do fluxo de pareamento. Máquina de estados:
`idle → starting → waiting → paired | error` (expira → `error`/`idle`).

- `Start(ctx, method, phone)`:
  - Cria device limpo: `container.NewDevice()`.
  - Novo client; anexa `handler.HandleEvent` + observador de pareamento.
  - `Connect()` (não autenticado); abre `GetQRChannel(ctx)`; aguarda o 1º evento.
  - Se `method=="phone"`: `PairPhone(ctx, phone, true, PairClientChrome, "Chrome (Linux)")`
    → guarda o código de 8 dígitos.
  - Consome `qrChan` numa goroutine, atualizando `qrCode`/`expiresAt` a cada rotação.
- Observador: em `events.PairSuccess`/`Connected` com `Store.ID != nil` →
  `holder.Set(novoClient)`, status `paired`.
- `Status()` → snapshot imutável (ver contrato REST).
- `Reset(ctx)`: `holder.Get().Logout(ctx)` (limpa device) + volta ao modo pareamento.
- Sessão expira em ~160s (limite do WhatsApp); QR rotaciona; `Status()` devolve o atual.

Concorrência: um pareamento por vez (mutex); `Start` durante `waiting` reinicia a
sessão. Client antigo é `Disconnect()`-ado ao criar o novo.

### 3. QR em PNG (server-side)

`rsc.io/qr` (já é dependência transitiva de `qrterminal` — sem import novo) gera o
QR a partir da string `evt.Code`; encoda em PNG → data-URI base64. O front recebe
`qr_png_base64` e só faz `<img src=...>`. Mantém o QR no stdout como fallback.

### 4. API — 3 endpoints admin-only

Interface `Pairer` injetada no `api.Server` (padrão consumer-defines-interface já
usado no pacote):

```go
type Pairer interface {
    Start(ctx context.Context, method, phone string) (PairingStatus, error)
    Status() PairingStatus
    Reset(ctx context.Context) (PairingStatus, error)
}
```

Rotas (todas `RequireAuth` + `requireAdmin` sobre o dono REAL da sessão; mutações
também `RequireOrigin` p/ CSRF):

- `POST /api/v1/admin/pairing/start` — body `{method:"phone"|"qr", phone?}`.
- `GET  /api/v1/admin/pairing/status` — front faz polling ~3s.
- `POST /api/v1/admin/pairing/reset`.

Contrato de resposta (`PairingStatus`):

```json
{
  "status": "idle|starting|waiting|paired|error",
  "method": "phone|qr|null",
  "pair_code": "ABCD1234 | null",
  "qr_png_base64": "data:image/png;base64,... | null",
  "expires_at": "RFC3339 | null",
  "connected_as": "+55… | null",
  "error": "mensagem | null"
}
```

Validação: `phone` obrigatório e só-dígitos quando `method=="phone"`; ≥7 dígitos,
sem prefixo `0` (a lib rejeita). Auditoria: registra quem disparou `start`/`reset` e
o `PairSuccess`.

### 5. `main.go` — startup não-bloqueante

Se `waClient.Store.ID == nil` no boot: **não** bloquear no stdout. Registrar o
`PairingManager` como pronto para parear (status `idle`), subir o HTTP normalmente.
O pareamento passa a ser dirigido pelos endpoints admin. Se já logado: `Connect()`
normal, manager fica `idle` com `connected_as` preenchido.

### 6. Página web — `/dashboard/admin/whatsapp`

Mesma proteção da admin atual (checagem `is_admin` server-side + gate real no
backend). Client component:

- Estado da conexão ("Conectado como +55…" | "Desconectado").
- **Gerar código**: campo do número do WhatsApp do bot → código de 8 dígitos grande
  + passo-a-passo (WhatsApp → Aparelhos conectados → *Conectar com número*).
- **Gerar QR**: `<img>` do QR pra escanear.
- Polling do status a cada ~3s; em `paired` → ✓ "Conectado".
- Link para a página a partir do painel admin existente.

## Segurança

Material de pareamento é sensível (quem completa o pareamento define a conta
WhatsApp do bot). Portanto: gate admin **no servidor** (não só esconder a página),
sessão de pareamento efêmera (160s), auditoria de start/reset/success. A checagem de
UI é espelho; o 403 real vem do backend.

## Testes

- `ClientHolder`: Get/Set concorrente, valor inicial nil.
- `PairingManager`: transições de estado; `Start(phone)` sem phone → erro de
  validação; `Status()` reflete code/qr; `PairSuccess` → `paired` + `holder.Set`
  chamado (client/handler fakes; sem rede real).
- QR PNG: string conhecida → PNG não-vazio decodificável, data-URI bem formada.
- API handlers: 401 sem sessão, 403 não-admin, 400 validação (padrão de
  `handlers_admin_test.go`), 200 com `Pairer` fake.

## Fora de escopo (YAGNI)

- SSE/websocket para status (polling basta p/ um admin).
- Múltiplas contas/números simultâneos.
- UI de histórico de pareamentos (auditoria fica no log).
