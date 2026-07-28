# Backfill de mensagens no reconecte

**Data:** 2026-07-27
**Status:** aprovado, em implementação

## Problema

Quando o WhatsApp do bot fica fora do ar (queda de conexão, número deslogado,
incidente de protocolo tipo o erro 400 da migração LID), os usuários continuam
mandando mensagens. O WhatsApp bufferiza essas mensagens e as entrega em bloco
quando o bot reconecta — via `events.HistorySync`, **não** via `events.Message`.
Hoje o bot só trata `*events.Message` ([handler.go:91](../../bot/handler.go));
`HistorySync` é ignorado. Resultado: tudo que chegou durante a queda é
silenciosamente perdido. Foi o que aconteceu no incidente de 17–23/jul (5 dias
sem responder): pedidos de agendamento, "tomei o remédio", perguntas — nada foi
respondido nem registrado.

## Objetivo

No reconecte, **responder automaticamente** o backlog de mensagens genuinamente
não-tratadas, reusando **exatamente** o mesmo caminho testado das mensagens ao
vivo (`handleMessage` → coalescência → orquestrador → agente), incluindo remédio
e agendamento. Sem responder o que já foi tratado, sem responder coisa antiga
demais, sem inundar o usuário.

## Não-objetivos (YAGNI)

- Reprocessar mensagens de **grupos**, broadcast, status ou de **não-usuários**
  (o `handleMessage` já descarta grupos; o backfill nunca aciona o funil de
  vendas para números desconhecidos — ver "Filtro de usuário").
- Reconstruir histórico de conversa para exibição — isto é só sobre **responder**
  o que ficou sem resposta.
- Backfill sob demanda / botão no painel. É automático no reconecte.

## Arquitetura

### Gatilho

Novo `case *events.HistorySync:` em `Handler.HandleEvent`
([handler.go:89](../../bot/handler.go)). O WhatsApp entrega `HistorySync` em
blobs logo após conectar — é o próprio mecanismo de entrega do backlog, então
não é preciso ouvir `Connected`/`OfflineSyncCompleted`. Cada blob:
`Data.GetConversations()` → por conversa, `GetMessages()` →
`[]*HistorySyncMsg`, e `msg.GetMessage()` → `*waWeb.WebMessageInfo`.

### Conversão (reuso do whatsmeow)

Para cada `WebMessageInfo`, `client.ParseWebMessage(chatJID, webMsg)` produz um
`*events.Message` **idêntico** ao de uma mensagem ao vivo
([client.go:914](https://github.com/tulir/whatsmeow) — conversor testado do
whatsmeow). Nada de parsing manual de protobuf. `chatJID` vem de
`conversation.GetId()`.

### Filtro de usuário (registrados/ativos apenas)

O `handleMessage` ao vivo, para número **desconhecido**, joga a mensagem no funil
do agente de vendas ([handler.go:222](../../bot/handler.go)). No backfill isso
seria péssimo: responder lead velho de dias atrás. Por isso o backfill
**pré-filtra**: resolve o remetente (`resolveSenderJID`, mesma lógica LID do
inbound), tenta `GetUserByPhone` (com as variantes de 9º dígito), e **só passa
adiante** se o usuário existe e está ativo. Mensagem de LID não-resolvido é
descartada com log (mesma postura do inbound). O `handleMessage` continua sendo
o executor final (e seus próprios guards — grupo, fromMe, dedup, lookup — são
rede de segurança).

### Marca d'água (dedup à prova de restart)

Chave da corretude. Por usuário:
`watermark(userID) = MAX(created_at) WHERE user_id=? AND role='user'` de
`conversation_history`. Como o orquestrador persiste a mensagem inbound **antes**
de rodar o agente ([orchestrator.go:59](../../bot/orchestrator.go)), a marca
d'água é o instante da **última mensagem que o bot efetivamente tratou**. Numa
queda, nada é tratado, então a marca fica no início da queda — exatamente a
fronteira desejada. Só processamos candidatos com **`Timestamp` (hora de envio)
> watermark** do respectivo usuário.

Novo método DB:
```go
// LastInboundAt devolve o created_at (UTC) da última mensagem role='user' do
// usuário, ou (zero, false) se ele nunca teve uma. Usado como marca d'água do
// backfill.
func (db *DB) LastInboundAt(userID int64) (time.Time, bool, error)
```
`created_at` é `CURRENT_TIMESTAMP` (UTC) no SQLite; a comparação é feita toda em
UTC (o `Timestamp` do WhatsApp também é UTC), sem conversão de fuso.

**Idempotência (cinturão + suspensório):** re-entrega do mesmo `HistorySync` no
**mesmo processo** é barrada pelo dedup em memória por MsgID do `handleMessage`
([handler.go:157](../../bot/handler.go)); re-entrega **após restart** (o cenário
de repareamento, em que o mapa em memória está vazio) é barrada pela marca
d'água — ao tratar um candidato, o orquestrador grava `role='user'` com
`created_at=now`, avançando a marca além de todo o backlog da queda. Um mutex no
handler serializa o processamento de blobs de `HistorySync` para dois blobs
concorrentes não lerem a mesma marca d'água antiga.

### Seleção (função pura, testável)

Núcleo isolado, sem DB nem cliente:
```go
type backfillCandidate struct {
    MsgID     string
    UserID    int64
    Timestamp time.Time     // hora de envio (do WebMessageInfo)
    FromMe    bool
    msg       *events.Message // já parseado; passado adiante a handleMessage
}

// selectBackfill escolhe o que processar, em ordem cronológica:
//   - descarta FromMe
//   - descarta Timestamp no futuro (> now, tolera skew pequeno)
//   - descarta Timestamp fora da janela (< now - ageCeiling)
//   - descarta Timestamp <= watermark[UserID] (já tratado)
//   - dedup por MsgID
//   - se passar de cap, mantém os `cap` MAIS RECENTES (e o caller loga o descarte)
func selectBackfill(cands []backfillCandidate, watermarks map[int64]time.Time,
    now time.Time, ageCeiling time.Duration, cap int) (selected []backfillCandidate, dropped int)
```
Parâmetros fixados: `ageCeiling = 24h`, `cap = 30`. O caller loga
`dropped > 0`. Os selecionados são alimentados em ordem cronológica a
`handleMessage`, que bufferiza/coalesce e responde igual ao vivo — **mídia
inclusa** (áudio/imagem passam pelo mesmo download/transcrição; mídia expirada no
servidor cai no fluxo de erro já existente do `handleMessage`, tradeoff aceito).

### Fluxo

```
events.HistorySync
  └─ (mutex do backfill)
     ├─ para cada conversa/WebMessageInfo:
     │    ParseWebMessage → resolveSenderJID → GetUserByPhone (ativo?)
     │    └─ registrado+ativo: LastInboundAt(userID) → monta candidate
     ├─ selectBackfill(candidates, watermarks, now, 24h, 30)
     ├─ log(dropped)
     └─ para cada selecionado (cronológico): handleMessage(cand.msg)
          └─ dedup/LID/coalescência/orquestrador/agente (idêntico ao vivo)
               └─ persiste role='user' (avança a marca d'água)
```

## Casos de borda

- **Primeiro pareamento de número novo:** nenhum usuário registrado ainda → nada
  a fazer. Só o **re**-pareamento de um bot existente tem usuários com histórico
  → marca d'água existe → preciso. O teto de 24h é o backstop.
- **Queda longa (>24h, ex.: incidente de 5 dias):** só as últimas 24h de cada
  usuário são respondidas; o resto é descartado por idade (responder "que horas
  são?" de 5 dias atrás é pior que silêncio).
- **Usuário sem histórico** (`LastInboundAt` = zero): marca d'água zero → passa
  tudo dentro das 24h. Correto.
- **Poda de histórico:** `AddConversationMessage` mantém as últimas 50 rows por
  usuário ([db.go:740](../../bot/db.go)); a marca d'água sobrevive enquanto houver
  ≥1 mensagem — sempre o caso num re-pareamento com queda recente.
- **Processo morre antes do flush do buffer:** nada persistido → marca não
  avança → próximo start re-seleciona e responde uma vez (at-least-once com dedup
  em memória; nunca duas respostas de fato).

## Testes

- **`selectBackfill` (unitário, puro):** fromMe descartado; futuro descartado;
  fora das 24h descartado; `<= watermark` descartado; `> watermark` e dentro da
  janela selecionado; dedup por MsgID; teto mantém os N mais recentes e reporta
  `dropped`; ordenação cronológica; watermark por usuário (usuário A não afeta B);
  usuário sem watermark passa dentro da janela.
- **`LastInboundAt` (integração DB):** devolve o MAX de role='user'; ignora
  role='assistant'; `(zero,false)` sem mensagens; UTC correto.
- **Integração do handler** (se viável com fakes): dois blobs concorrentes de
  HistorySync não duplicam (mutex + dedup); número desconhecido não vira lead.

## Deploy

Feature outward-facing e sensível (auto-responde usuários reais, inclui remédio).
Deploy **só com o bot online** (guard de reconexão do runbook), mesmo padrão do
#1: `docker builder prune` → build → `docker compose up -d`, via SSM. Observar os
logs do primeiro reconecte pós-deploy para confirmar volume e ausência de
respostas indevidas.
