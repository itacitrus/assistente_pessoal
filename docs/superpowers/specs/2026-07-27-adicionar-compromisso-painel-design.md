# Adicionar compromisso pelo painel

**Data:** 2026-07-27
**Status:** aprovado, em implementação

## Problema

O painel **mostra** a agenda (Google Calendar) mas não tem ação de **adicionar**
compromisso. Quando um pedido de agendamento chega fora do fluxo de WhatsApp (ex:
mensagem perdida durante uma queda do bot), o operador não consegue lançar o evento
pela UI — só o bot, por conversa. Falta um "adicionar compromisso" no painel, que
funcione também no "ver como" (admin lança para qualquer titular).

## Objetivo

Botão **"Adicionar compromisso"** na agenda que cria um evento no Google Calendar do
titular, reusando o `CreateEvent` já testado do bot. Opcionalmente avisa o titular no
WhatsApp. Funciona sob impersonação ("ver como").

## Arquitetura

Reusa a infra existente — nada de reimplementar calendário ou fuso.

### Backend

- **`calendarReader` (interface do adapter)** ganha:
  `CreateEvent(ctx, refreshToken, calendarID string, ev CalendarEvent) (*CalendarEvent, error)`.
  O `*CalendarClient` concreto já implementa; a interface só passa a expor.
- **`apiAdapter.CreateAgendaEvent(ctx, userID, in)`** (novo): pega o usuário; se não
  tem Google conectado → `ErrNoCalendar`; decifra o refresh token; resolve o fuso do
  titular com `db.GetEventTimezone(userID, quando)` (respeita viagem ativa, default
  BRT); monta `CalendarEvent{Title, Start, End: Start+duração, Timezone}`; chama
  `cal.CreateEvent`. Se `in.Notify` → `a.sendMsg(user.PhoneNumber, msg)` (o mesmo
  `handler.SendTextToPhone`, que persiste em `conversation_history`), setando
  `Notified`. Falha no aviso **não** derruba a criação — evento é o resultado primário.
  Mesmo caminho de decifra/fuso do `UpcomingEvents`.
- **Contrato (pacote api):**
  ```go
  type CreateEventInput  struct { Title, Date, Time string; DurationMin int; Notify bool }
  type CreateEventResult struct { Event AgendaEvent; Notified bool }
  // Store: CreateAgendaEvent(ctx, userID int64, in CreateEventInput) (CreateEventResult, error)
  ```
  `Date` = "YYYY-MM-DD", `Time` = "HH:MM". O adapter localiza (`ParseInLocation`) no
  fuso do titular. `DurationMin` default 60.
- **Handler** no path **`/api/v1/me/agenda/events`**, despachando por método:
  - `GET` → listar (comportamento atual, inalterado).
  - `POST` → criar. Usa o **usuário efetivo** (`userFromContext`) — "ver como" lança na
    agenda da pessoa vista. Valida título não-vazio, `Date`/`Time` bem-formados,
    `DurationMin` 1..1440 (default 60). Enforce **Origin** (CSRF) no POST (inline, já
    que GET no mesmo path não pode exigir Origin). Mapeia `ErrNoCalendar` → 409 com
    mensagem clara ("Conecte o Google Calendar primeiro."). Auditoria
    `agenda_event_created`. Responde `{ event, notified }`.

### Frontend (`/dashboard/agenda`)

- Botão **"Adicionar compromisso"** abre um **formulário inline** (o projeto não tem
  primitivo de Dialog; form colapsável segue o padrão atual): **título**, **data**,
  **hora**, **duração** (default 1h) e checkbox **"avisar no WhatsApp"** (default
  **desmarcado** — evita mensagem-surpresa; o operador marca quando quer).
- Submete → `POST /me/agenda/events` → em sucesso, fecha o form e **recarrega o
  calendário** mostrando o novo evento. Se `notified=false` e o usuário pediu aviso,
  informa "criado, mas não consegui avisar no WhatsApp".
- Funciona no "ver como" automaticamente (a página renderiza como o usuário efetivo).

## Segurança / consistência

- Só o titular ou um admin em "ver como" cria (auth + efetivo). Origin no POST.
- O lembrete automático **1h antes** sai sozinho: o scheduler lê o Google Calendar, e o
  evento criado aqui é idêntico ao criado pelo bot. Sem código de lembrete novo.
- Aviso opcional passa por `SendTextToPhone` → persiste em `conversation_history`
  (invariante do projeto: toda fala do bot entra no histórico).

## Testes

- **Adapter** (`CreateAgendaEvent`): fuso correto (data+hora localizadas em BRT vs
  fuso de viagem), `End = Start + duração`, `Notify=true` chama `sendMsg`, `Notify=false`
  não; Google desconectado → `ErrNoCalendar`. Fakes de `calendarReader` e `sendMsg`.
- **Handler** (`POST /me/agenda/events`): 401 sem sessão; 403 sem Origin; 400 título
  vazio / data-hora inválida; 409 sem Google; 200 + `{event,notified}` no sucesso;
  usa o usuário efetivo (impersonação). `GET` continua funcionando (não regrediu).

## Fora de escopo (YAGNI)

- Local, convidados, recorrência, editar/excluir pela UI (o bot por WhatsApp cobre;
  excluir já existe no fluxo do bot). Só criação rápida aqui.
