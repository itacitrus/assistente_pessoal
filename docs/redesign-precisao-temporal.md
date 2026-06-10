# Zello — Diagnóstico unificado e arquitetura definitiva para as 5 falhas de precisão

Documento de engenharia • Bot Go (whatsmeow) • engine dual: operacional = Anthropic Sonnet (`runLoop`), companion/idoso = DeepSeek (`runCompanion`/`runLoopLLM`) • `buildMessages`/`buildMessagesLLM` + system prompt multi-parte com prompt caching • coalescing por usuário em `handler.go` • `scheduler` + `scheduler_medication` disparando proativos.

Todas as referências de arquivo/linha abaixo foram verificadas contra o código atual (workflow de verificação adversarial, 11 agentes).

---

## A. Diagnóstico unificador — as causas-raiz por trás dos 5 sintomas

Os 5 bugs não são 5 problemas independentes. São 5 manifestações de **5 falhas estruturais**, e uma delas sozinha (grounding temporal) está por baixo de 3 dos 5 sintomas. O padrão comum: **pedimos ao LLM para *derivar* um fato que o Go já tem em mãos, e não verificamos a saída antes de enviar.**

### As causas-raiz (R1–R5)

**R1 — Contexto temporalmente "cego" (untimestamped context).**
Toda informação temporal é apagada antes de o modelo vê-la. `buildMessages` (agent.go:364-388) e `buildMessagesLLM` (agent_companion.go:209-226) descartam `ConversationMessage.CreatedAt` — que o banco **já traz** (`GetConversationHistory` faz `Scan(&m.Role, &m.Content, &m.CreatedAt)`, db.go:751). O `FormatReminder` (formatter.go:87-90) emite só `"15:00"`, sem dia. O único relógio é `"Data/hora atual"` no `buildSystemPromptDynamic`, em inglês (`(Monday)`). O modelo então **chuta** quando um turno passado aconteceu e em que período do dia estamos.

**R2 — LLM forçado a derivar fato que o Go deveria computar.**
Período do dia, dia-da-semana, "ontem vs. amanhã", instante absoluto de um horário — tudo deixado para o modelo inferir, em vez de o Go asseverar. É a violação direta do princípio do dono: *compute o fato temporal em Go e injete como asserção explícita; não peça ao LLM para derivá-lo.*

**R3 — Ausência de output-guard determinístico.**
Não existe nenhuma camada entre a geração e o envio que verifique invariantes. A saída do `runLoop`/`runCompanion` vai direto para o transporte. Nada impede um "🌙 boa noite" às 07:20, nem um "pode deixar que te aviso às 23:58" sem tool, nem narrar uma duplicata. O único precedente correto que existe é `reconcileTakenAfterTurn` (agent_companion.go:62) — uma salvaguarda pós-turno para medicação. **Esse é o padrão a generalizar.**

**R4 — Gaps de capacidade que o modelo "tapa" com confabulação.**
Não existe primitivo de lembrete pontual: sem tabela, sem tool, sem job. Quando o usuário pede "me lembre às 23:58", o modelo, sem ferramenta, **promete em prosa** algo que o sistema não consegue cumprir. É a falha B4 inteira.

**R5 — Artefatos de transporte vazando para a persona.**
O coalescing buffer junta mensagens com `strings.Join(pb.texts, "\n")` (handler.go:298) **sem checagem de conteúdo**. Dois toques (mesmo texto, IDs distintos) passam o gate de dedup-por-ID (handler.go:121-132) e produzem uma duplicata *literal* dentro de um único turno. O modelo vê a duplicata real e a racionaliza narrando o encanamento ("veio em dobro, mas criei só uma vez").

### Mapa bug → causa-raiz

| Bug (palavras do dono) | R1 cego temporal | R2 LLM deriva | R3 sem guard | R4 gap capacidade | R5 transporte |
|---|:---:|:---:|:---:|:---:|:---:|
| **B1** super-pergunta nome / ignora "salva como falei" | | | ● (secundário) | | |
| **B2** confunde hoje/amanhã, conflito fantasma | ● **(primário)** | ● | ● | | |
| **B3** "boa noite" às 7h | ● | ● **(primário)** | ● | | |
| **B4** promete lembrar 23:58 e não lembra | ● | ● | ● | ● **(primário)** | |
| **B5** alucina mensagem duplicada | | | ● | | ● **(primário)** |

Leitura-chave: **R1+R2 (grounding temporal) tocam B2, B3 e B4** — a alavanca mais alta do conjunto. B1 e B5 sobram como fixes locais (prompt e transporte). R3 (output-guard) é a rede transversal que protege B2, B3, B4 e B5 de regressão. B1 é majoritariamente uma lacuna de *completude do prompt/schema* (o modelo inventou uma precondição de "nome completo" que **não existe** em `handleCriarEvento`, tools.go:204-241).

---

## B. A arquitetura — "aprimorando o prompt, criando skills ou cadeias de agentes, mas garanta que a saída vai ser correta"

A resposta tem **quatro camadas**, em ordem de garantia decrescente (a primeira é a que mais "garante a saída"):

### B.1 — Camada de Temporal Grounding em Go (mata R1+R2 → B2, B3, B4)

Princípio: **o Go pré-computa todos os fatos temporais e os injeta como asserções explícitas. O modelo nunca mais infere "quando" nem "que período é".**

**(a) Asserções de "agora" — no bloco DINÂMICO, nunca no cacheado.**
Reescrever `buildSystemPromptDynamic` (agent.go:590) para emitir, em PT-BR e computado em Go:

```
Data/hora atual: 2026-06-09 07:20 (segunda-feira, manhã) (fuso: America/Sao_Paulo).
```

Trocar o `Format("...(Monday)")` inglês por `ptWeekday(t.Weekday())` (reusa `weekdaysPT`, formatter.go:9) + `periodOfDay(t.Hour())`. Beneficia **ambas** engines (operacional e companion via `systemPartsToLLM`).

**(b) Bloco `[PERÍODO DO DIA]` determinístico para idoso — novo system part dinâmico.**
Novo `periodOfDay(hour int) string` → `madrugada` (0-4), `manhã` (5-11), `tarde` (12-17), `noite` (18-23). Novo `appendCompanionTimeOfDayPart` (no-op para não-idoso) resolve o fuso local via `GetEventTimezone(user.ID, now)` (já usado em agent_companion.go:250/311; fallback `BRT()`), computa o período e emite **uma** asserção que governa abertura **e** fechamento, em **todas** as horas:

```
[PERÍODO DO DIA] Agora é MANHÃ (07:20, fuso local). Proibido "boa noite",
"boa tarde", "descanse bem", "até amanhã" e o emoji 🌙. Saudação é "bom dia".
Despedida de FIM DE DIA só quando o período for NOITE.
```

Switch por período (cada um proíbe as saudações/despedidas dos outros). Encaixe em `Agent.Run` logo após os outros injects time-aware (agent.go:170-171), **antes** de `appendCompanionContinuationPart`.

**(c) Lembrete auto-datado.**
Reescrever `FormatReminder` (formatter.go:87-90) para usar `relativeDayLabel(ev.Start, now)` (já existe, formatter.go:23) + `weekdaysPT`:
> `Lembrete: *Reunião devs Itacitrus* HOJE (Terça, 03/06) às 15:00 (em 1 hora)`

Assim o artefato mais relido no histórico carrega a própria data para sempre.

**(d) Carimbo relativo por turno de histórico — o fix load-bearing de B2.**
Novo helper compartilhado `formatHistoryTurn(content string, createdAt, now time.Time) string`: converte `createdAt` (driver modernc-sqlite devolve em UTC) para BRT, compara o dia calendário com `now` e prefixa:
- mesmo dia → `[hoje 09:12] `
- dia anterior → `[ontem 14:00] `
- dia seguinte → `[amanhã 08:00] ` (defensivo)
- caso contrário → `[ter 03/06 14:00] `
- `createdAt.IsZero()` → retorna `content` **sem prefixo** (compat com ~40 testes que não setam `CreatedAt`).

Chamar de **ambos** os serializadores: `buildMessages` (agent.go:374-377) e `buildMessagesLLM` (agent_companion.go:219). Assinaturas ganham `now time.Time`; callers (`Run` agent.go:128 e `runCompanion` agent_companion.go:48) passam `time.Now()`.

**Onde isso vive e implicação de cache (crítico):** os carimbos por turno entram no **array de MESSAGES** (conteúdo user/assistant), **não** no system prompt — então o prefixo cacheado de `buildSystemPromptStable` (ephemeral, agent.go:158-161) e `markLastMessageForCache` ficam **intactos**; hit rate >70% preservado. As asserções de "agora" e `[PERÍODO DO DIA]` vão no bloco **dinâmico** (não cacheado) — porque mudam de hora em hora. Custo: ~6-12 chars por linha de histórico. Negligenciável.

### B.2 — Cadeia de verificação / OUTPUT-GUARD determinístico (R3 — "garanta que a saída vai ser correta")

A "cadeia de agentes" que o dono pediu, no formato certo: **um estágio determinístico em Go que roda APÓS a geração e ANTES do envio**, generalizando o padrão já provado de `reconcileTakenAfterTurn`. Roda em `Run` (após `runLoop`) e em `runCompanion` (após `runLoopLLM`). Recebe `(user, finalText, toolsCalled, now, period, canonicalEventDates)`.

| # | Invariante | Tipo de check | Ação na violação |
|---|---|---|---|
| I1 | Saudação/despedida não pode contradizer o período (`🌙`/"boa noite"/"descanse bem" fora da noite; "bom dia" à noite) | **Determinístico** (regex sobre `periodOfDay`) | **Regenerar 1×** com feedback → se persistir, **strip** da frase |
| I2 | Nenhuma afirmação de item criado/salvo/agendado ("cadastrei", "anotei", "marquei", "te aviso às HH") sem tool de sucesso **neste turno** | **Determinístico** (regex de claim × `toolsCalled`) | **Auditar** `*_promise_ungrounded` + **regenerar 1×**; nunca fabricar dado |
| I3 | Não narrar mecânica de transporte (duplicação, reenvio, junção, atraso de rede) | **Determinístico** (lista de frases) | **Strip** silencioso da sentença |
| I4 | Palavra de data relativa na resposta ("amanhã", "hoje") bate com a data canônica do evento do turno | **Determinístico** quando há `canonicalEventDates`; **critic LLM leve** só sem âncora de tool | Determinístico: regenerar com a data certa. Critic: só **flag** |

**Default da ação: regenerar 1× com feedback** para I1/I2/I4; **strip** para I3; **block** nunca isolado (o usuário sempre recebe *alguma* resposta).

### B.3 — Fix de capacidade: lembrete pontual (R4 → B4)

O modelo promete porque **não tem a ferramenta**. Construir o primitivo, não instruir o modelo a se conter.

- **Tabela `reminders`** (novo `CREATE TABLE`, não `ALTER`): `id`, `user_id` (FK ON DELETE CASCADE), `text`, `fire_at` (instante UTC absoluto), `status` CHECK(`pending`/`sent`/`canceled`), `origin`, timestamps; `UNIQUE(user_id, fire_at, text)` (dedup de restart); índice `(status, fire_at)`.
- **`db_reminders.go`** (espelha `db_medication.go`): `CreateReminderIfAbsent` (UNIQUE→`ErrReminderDuplicate` idempotente), `GetDueReminders(now)` com `fire_at <= now` (**catch-up-safe**), `MarkReminderFired` com guard `status='pending'`, `CancelReminder`/`ListPendingReminders`.
- **Tool `agendar_lembrete`** (registrada em `buildToolHandlers`, tools.go:47 → ambas engines via `toolDefsToLLM`). Desacoplada do Google Calendar — funciona para idoso **sem agenda conectada**. O instante é resolvido pelo **mesmo `ResolveEventDate`** que `criar_evento` usa (date_resolver.go:29). O modelo passa a string literal + `date_source`; o retorno ecoa o horário **resolvido** ("Lembrete salvo: vou te avisar hoje às 23:58."). Schema deixa claro: *"NUNCA prometa lembrar em texto sem chamar esta tool."*
- **`scheduler_reminders.go`** — novo `checkAdHocReminders` registrado em `Start()` (scheduler.go:69). Short-circuita sem notifier (mesmo guard de `checkMedicationReminders`). Roteia idoso pelo `Notifier` (persistência em `conversation_history` via `persistOutbound`). **`MarkReminderFired` só após envio bem-sucedido** → falha de transporte re-dispara no próximo tick.
- **Grounding:** regra dura em `companionCoreTemplate` e no operacional + invariante I2 do output-guard como rede determinística.

### B.4 — Fixes prompt-only que sobram (B1 e parte de B5)

**B1 (super-pergunta de nome):** completude de prompt+schema, não validação de código — `handleCriarEvento` **já persiste qualquer Title** (tools.go:204-241); um gate de "nome completo" *codificaria* o bug. Três pontos:
1. **Schema da tool** `criar_evento.title` (agent.go:632) — ponto routing-proof, cache-resident, que chega a Sonnet **e** DeepSeek: *"Use EXATAMENTE o identificador fornecido — um apelido como 'pupuzinho' JÁ É título válido e completo. NUNCA exija nome completo/sobrenome/nome real. Se disser 'salva como X', use X literalmente."*
2. **`buildSystemPromptStableOperational`** (bloco APELIDO após RECORRÊNCIA): apelido já é nome; obedecer instrução literal; aniversário (mesmo de ontem) → `is_birthday=true` (rota que aceita data passada em tools.go:223-241, fugindo do erro `data explicita no passado` de date_resolver.go:62).
3. **`companionCoreTemplate`** (paridade entre engines).

**B5 (duplicata):** correção real é determinística no transporte. Novo `dedupCoalescedTexts([]string) []string` puro, chamado em `flushBuffer` **imediatamente antes** do `Join` (handler.go:298). Colapsa duplicatas normalizadas (`ToLower(TrimSpace(t))`) **dentro da mesma janela de coalescing**, preservando ordem e 1ª ocorrência. Único chokepoint dos 3 consumidores. Não toca o dedup-por-ID; não colapsa entre turnos (repetições legítimas honradas — "believe-the-user"). Prompt entra **só como defense-in-depth** (linha em ambos os stable prompts + invariante I3).

---

## C. Cross-cutting — interações e resolução

| Interação / risco | Resolução |
|---|---|
| **Carimbos incham o stable prompt cacheado?** | **Não.** Carimbos vão no array de MESSAGES (per-turn), não no system. `buildSystemPromptStable` (agent.go:158) e `markLastMessageForCache` intactos. Asserções de "agora"/período vão no bloco **dinâmico** (já não cacheado). Zero cache thrash. |
| **Edição do schema da tool (B1) quebra cache?** | One-time: `buildToolDefinitions` está no prefixo cacheável; **um** cache miss no deploy, depois retoma hit alto. |
| **Output-guard adiciona latência no caminho do idoso?** | I1/I2/I3 são **regex puros** — microssegundos. Só I4-sem-âncora chamaria critic LLM, por isso fica em **flag/auditar**. Regenerate (I1/I2) só em **violação real** (raro) e custa 1 chamada. |
| **Content-dedup derruba repetição legítima?** | Escopo estrito: só **dentro de uma janela de coalescing** (<9s idoso). Eventos diferentes diferem no texto → preservados. Repetições em turnos separados não são afetadas. |
| **`is_birthday` forçado (B1) colide com conflito (B2)?** | Não. Aniversário é all-day yearly (tools.go:223-241) e nem entra no windowing de conflito. |
| **Gate removido de `appendCompanionDayContextPart` (B3) contradiz `[PERÍODO DO DIA]`?** | Scoping: branch "sem lembretes" só sugere despedida fim-de-dia quando `periodOfDay==noite`; de manhã, encerramento aberto neutro. As três asserções concordam. |
| **Inconsistência latente (NÃO corrigida aqui):** branch de aniversário retorna `FormatEventCreated` direto (tools.go:240) enquanto o normal retorna `OK_CRIADO|display=` (tools.go:419) que a REGRA DE CITAÇÃO (agent.go:471) ancora. | Aniversário **bypassa** o contrato de citação verbatim. Bug latente de *display consistency*, **fora** do escopo de B1; anotado para follow-up. |

---

## D. Plano faseado — P0 = maior alavanca / menor risco

**A única mudança que mata mais bugs: a Camada de Temporal Grounding (P0).** Carimbos por turno + período em Go atacam B2, B3 e B4 de uma vez, são puramente aditivos e testáveis em Go sem LLM.

### P0 — Temporal Grounding em Go (mata B2 e B3; pré-requisito de B4)
- **Ships:** `periodOfDay`, `formatHistoryTurn`, `FormatReminder` datado, `buildSystemPromptDynamic` PT-BR, `appendCompanionTimeOfDayPart`, carimbos em `buildMessages`+`buildMessagesLLM` (+`now`), remoção do gate `Hour()<14` em `appendCompanionDayContextPart`.
- **Arquivos:** `agent.go`, `agent_companion.go`, `formatter.go`.
- **Gate:** `TestPeriodOfDayBoundaries`, `TestFormatHistoryTurn_RelativePrefixes`, `TestBuildMessages{,LLM}_StampsHistoryTimestamps`, `TestFormatReminder_IsDateExplicit`, `TestAppendCompanionTimeOfDayPart_*`, `TestBuildSystemPromptDynamic_LocalizedWeekdayAndPeriod`, `TestFormatHistoryTurn_BackwardCompatZeroTime`. **Todos determinísticos.**

### P-transporte (pode ir junto de P0) — mata B5
- **Ships:** `dedupCoalescedTexts` + chamada em `flushBuffer:298` + log de drop.
- **Arquivos:** `handler.go`, `handler_test.go`.
- **Gate:** `TestDedupCoalescedTexts_*` (7 casos) + `TestFlushBuffer_PersistsSingleTurnOnDoubleTap`.

### P1 — Output-guard determinístico (rede de B2/B3/B4/B5; observabilidade)
- **Ships:** estágio pós-geração I1-I4; `reconcilePromisedReminderAfterTurn` (audita `reminder_promise_ungrounded`).
- **Arquivos:** `agent.go`, `agent_companion.go`, novo `output_guard.go`.
- **Gate:** `TestReminderPromiseSafeguard_*`, I1 (despedida noturna de manhã → strip), I3 (frase de duplicata → strip).

### P2 — Capacidade de lembrete pontual (mata B4)
- **Ships:** tabela `reminders`, `db_reminders.go`, tool `agendar_lembrete`/`cancelar_lembrete`, `scheduler_reminders.go`, grounding no prompt.
- **Arquivos:** `db.go`, novos `db_reminders.go`/`tools_reminders.go`/`scheduler_reminders.go`, `tools.go`, `agent.go`, `scheduler.go`, `prompts_companion.go`.
- **Gate:** `TestAgendarLembrete_PersistsBeforeConfirming`, `TestCheckAdHocReminders_FiresAtDueTime_Idempotent`, `TestCheckAdHocReminders_CatchUpAfterMissedTick`, `TestAgendarLembrete_SendFailureRefiresNextTick`, `TestResolveReminderInstant_PastTimeRollsToTomorrow`.

### P3 — Fixes de prompt (mata B1; defense-in-depth de B5)
- **Ships:** schema `title` de `criar_evento`, bloco APELIDO no operacional + worked example, invariantes no `companionCoreTemplate`; linha defense-in-depth de B5.
- **Arquivos:** `agent.go`, `prompts_companion.go`.
- **Gate:** `TestOperationalPromptForbidsFullNameOverAsk`, `TestCompanionCoreForbidsFullNameOverAsk`, `TestCriarEventoTitleSchemaCarriesNicknameRule`, `TestBirthdayHandlerAcceptsNicknameTitleAndPastDate`, `TestNoPromptOrSchemaRequestsFullName`.

---

## E. Matriz consolidada de testes

**Legenda:** `[Go]` = teste determinístico em Go; `[Eval]` = asserção de prompt/eval.

### Camada 1 — Temporal Grounding `[Go]`
| Teste | given → expect |
|---|---|
| `TestPeriodOfDayBoundaries` | hours 0..23 → madrugada(0-4)/manhã(5-11)/tarde(12-17)/noite(18-23); bordas 4/5,11/12,17/18,23/0 |
| `TestFormatHistoryTurn_RelativePrefixes` | createdAt {hoje, ontem, amanhã, 6d atrás, zero} em UTC → `[hoje 09:12] `/`[ontem 14:00] `/`[amanhã 08:00] `/`[ter 03/06 10:00] `/inalterado; 17:00Z→14:00 BRT |
| `TestFormatHistoryTurn_BackwardCompatZeroTime` | `CreatedAt` zero → conteúdo verbatim, sem prefixo, sem panic |
| `TestFormatReminder_IsDateExplicit` | evento hoje 15:00, now mesmo dia → título + `15:00` + `HOJE (Terça, 03/06)`; tabela HOJE/AMANHÃ/outro-dia |
| `TestBuildMessages_StampsHistoryTimestamps` | hist=[lembrete dateless, `CreatedAt`=ontem 17:00Z], now=hoje 10:00 BRT → msg começa `[ontem 14:00] `; turno atual sem prefixo |
| `TestBuildMessagesLLM_StampsHistoryTimestamps` | idem via serializador companion → mesmo `[ontem 14:00] ` (paridade DeepSeek) |
| `TestBuildSystemPromptDynamic_LocalizedWeekdayAndPeriod` | now=2026-06-09 07:20 BRT → contém `segunda-feira`, `manhã`, `2026-06-09 07:20`; **não** contém `Monday` |
| `TestAppendCompanionTimeOfDayPart_MorningForbidsNightClosing` | idoso, now=07:20 → contém `MANHÃ` + `boa noite`/`descanse bem`/`🌙` dentro de PROIBIDO |
| `TestAppendCompanionTimeOfDayPart_EveningAllowsNightClosing` | idoso, now=21:00 → `NOITE`; "boa noite"/"descanse bem" **fora** de PROIBIDO |
| `TestAppendCompanionTimeOfDayPart_NonIdosoNoop` | UserTypeComum → 0 parts |
| `TestAppendCompanionDayContextPart_MorningStillInjectsWhenRemindersRemain` | idoso, med 19:00, now=07:20 → 1 part com "ainda há lembrete" (gate removido) |
| `TestCriarEvento_ConflictCheckWindowUnchanged` | evento hoje 15-16h; novo amanhã 16-17h → `ListEvents` com janela amanhã 16-17h; sem CONFLITO |

### Camada 2 — Output-guard `[Go]` (+ 1 eval)
| Teste | given → expect |
|---|---|
| `TestReminderPromiseSafeguard_DetectsUngroundedPromise` `[Go]` | "te lembro às 23:58" com `toolsCalled=[]` → flag/audita; negativos (criar_evento/agendar_lembrete chamado) → não flag; nunca fabrica row |
| `TestGuard_NightGreetingAtMorningStripped` `[Go]` | "bom dia 🌙 descanse bem", period=manhã → I1: regenerate-then-strip |
| `TestGuard_TransportArtifactNarrationStripped` `[Go]` | "veio em dobro, mas criei só uma vez" → I3 strip silencioso |
| `TestConflictWarning_NotEmittedFromHistory` `[Eval]` | hist `[ontem 14:00] Lembrete...15:00`; user "amanhã 16h madre tereza"; buscar_agenda vazio → cria amanhã 16h, **sem** aviso de conflito |

### Camada 3 — Capacidade de lembrete `[Go]`
| Teste | given → expect |
|---|---|
| `TestAgendarLembrete_PersistsBeforeConfirming` | idoso sem GCal, "me lembre às 23:58", now=22:10 → row `pending`, `fire_at`=23:58 BRT (UTC), via `ResolveEventDate`; resultado cita "23:58" |
| `TestCheckAdHocReminders_FiresAtDueTime_Idempotent` | 1 row due; rodar 2× → exatamente 1 envio; após 1ª `status='sent'` |
| `TestCheckAdHocReminders_CatchUpAfterMissedTick` | `fire_at`=now-3min → ainda dispara (`fire_at <= now`) |
| `TestAgendarLembrete_SendFailureRefiresNextTick` | `Send` erra na 1ª, ok na 2ª → row segue `pending`, re-envia, 1 entrega total |
| `TestResolveReminderInstant_PastTimeRollsToTomorrow` | inferred '23:58'@22:10→hoje; '08:00'@22:10→amanhã; explicit hoje+passado→amanhã+AdjustNote |
| `TestAgendarLembrete_EmptyOrUnparseable_AsksUser` | text='' ou time='depois' → pede esclarecimento; nenhum row; **não** IsError |

### Camada 4 — Prompt/schema `[mistas]`
| Teste | given → expect | Tipo |
|---|---|---|
| `TestCriarEventoTitleSchemaCarriesNicknameRule` | `title.description` contém 'apelido', 'NUNCA exija nome completo', cláusula 'salva como X' | `[Go]` |
| `TestOperationalPromptForbidsFullNameOverAsk` | contém apelido-suficiente, 'NUNCA' perto de 'nome completo'; negativo: sem instrução de pedir nome completo | `[Go]` |
| `TestCompanionCoreForbidsFullNameOverAsk` | mesmas invariantes no registro idoso | `[Go]` |
| `TestNoPromptOrSchemaRequestsFullName` | prompt op + companion + schema → zero ocorrências de exigir 'nome completo' como precondição | `[Go]` |
| `TestBirthdayHandlerAcceptsNicknameTitleAndPastDate` | `{title:'Aniversario Pupuzinho', is_birthday:true, date:ontem}` → sem erro; EventType=='birthday'; Start=ontem | `[Go]` |
| `TestExplicitPastDateNonBirthdayErrors` | `ResolveEventDate(Explicit, ontem)` → erro 'data explicita no passado' | `[Go]` |
| `TestOperationalPromptHasNicknameWorkedExample` | exemplo "aniversario do pupuzinho"+"salva" → is_birthday=true, title 'Pupuzinho' | `[Eval/Go]` |

### Camada transporte — dedup `[Go]`
| Teste | given → expect |
|---|---|
| `TestDedupCoalescedTexts_ExactDoubleTap` | `["Cortar cabelo seg 8:00","Cortar cabelo seg 8:00"]` → 1 entrada |
| `TestDedupCoalescedTexts_CaseAndWhitespaceDoubleTap` | `["Cortar cabelo","  cortar cabelo "]` → 1 entrada, forma original |
| `TestDedupCoalescedTexts_DistinctMultiPartNotCollapsed` | `["Bom dia","Tudo bem?","Pronto"]` → 3 inalteradas |
| `TestDedupCoalescedTexts_NonAdjacentRepeatBySeenSet` | `["agendar dentista","que horas são?","agendar dentista"]` → 2 entradas |
| `TestDedupCoalescedTexts_WhitespaceAndEmptyEntries` | `["","   ","oi","oi"]` → `["oi"]` |
| `TestDedupCoalescedTexts_DifferentEventsInWindowPreserved` | `["marcar reunião 10h","marcar reunião 14h"]` → ambas |
| `TestFlushBuffer_PersistsSingleTurnOnDoubleTap` | 2 eventos texto idêntico, IDs distintos → `Process` 1× com 1 linha; 1 row `user` |

### Co-ocorrência farma (B3) `[Go]`
| `TestReconcileTakenAfterTurn_ObrigadaIsNotTaken` | pending; user "Obrigada"; toolsCalled=[] → no-op (não marca dose) |

---

## F. O que shipar primeiro

**P0 — a camada de Temporal Grounding em Go — junto do dedup de transporte.** Maior alavanca pelo menor risco: uma única família de mudanças puramente aditivas e 100% testáveis em Go (sem LLM no CI) elimina B2 e B3 de imediato, destrava B4, e o dedup mata B5 — quatro dos cinco sintomas — sem tocar o prefixo cacheado nem o schema do banco. É a materialização literal do princípio do dono ("compute o fato temporal em Go, não peça ao LLM para derivá-lo"): trocamos *esperança de prompt* por *asserção determinística*. Em seguida P1 (output-guard) como rede + observabilidade, depois P2 (lembrete) e P3 (prompt de B1).

Arquivos centrais: `agent.go` · `agent_companion.go` · `formatter.go` · `handler.go` · `date_resolver.go` · `tools.go` · `db.go` · `scheduler.go` · `prompts_companion.go` · novos: `db_reminders.go`, `tools_reminders.go`, `scheduler_reminders.go`, `output_guard.go`.

---

# ADENDO — Contrato de implementação (pós-painel de design review)

Veredito do painel (4 revisores adversariais + árbitro, todas as alegações re-verificadas contra o código): **definitivo COM emendas**. A arquitetura de 4 camadas fica; as emendas abaixo são VINCULANTES e substituem o texto acima onde conflitarem.

## Decisões estruturais

- **[S1] Regenerate do guard = UMA reescrita tool-less.** Anexa ao MESMO transcript do loop (com tool_use/tool_result — o modelo vê o que foi commitado) um turno assistant com o draft violador + um turno user `[CORREÇÃO DO SISTEMA] ...`, e faz UMA chamada com Tools omitido. Reescrita ainda viola/tenta tool → strip determinístico. Strip deixa vazio/<N chars/violador → safe-template período-correto em Go. Guard retorna `(text, action)`, provadamente não-vazio. Turno >20s → pula regenerate. Turno sintético nunca persiste.
- **[S2] Dedup do turno atual ANTES dos carimbos (em P0).** `Orchestrator.Process` passa a `Run` a string exata persistida; `Run`/`runCompanion` descartam a ÚLTIMA row do histórico quando `role=="user" && content==persistedContent`. Persist-antes-de-Run mantido (audit snippet de handleCriarEvento depende).
- **[S3] TurnContext mínimo temporal:** `{Now, Loc, Period, ContinuationAgeSec, ContinuationOK, UpcomingMedsToday}` computado UMA vez no topo de Run/RunProactive. Um turno = um now = um fuso = um período. `buildSystemPromptDynamic` ganha now+loc por parâmetro. Partes temporais = renderers puros; guard recebe o MESMO TurnContext.
- **[S4] Guard + asserção de período cobrem Run, runCompanion E RunProactive.** `runLoop` estendido para `(text, toolsCalled, toolResults, err)`.

## Deltas por fase

**P0 (GO com deltas):** S2+S3 dentro; carimbos/relógio no fuso do USUÁRIO via GetEventTimezone (travel-aware, fallback BRT); fundir [PERÍODO DO DIA]+[CONTEXTO DO DIA]+[CONTINUAÇÃO] em UMA parte `[AGORA]` (redação positiva-primeiro, SEM lista enumerada de tokens proibidos; madrugada herda despedidas da noite); mitigação de eco em P0 (linha nos 2 stable prompts + strip determinístico `^\[(hoje|ontem|...)...\]` nas 3 saídas); artefatos persistidos absoluto-primeiro `"Terça, 03/06 (HOJE) às 15:00"` + linha interpretativa; `ORDER BY created_at DESC, id DESC` (GetConversationHistory, Search, trim); teste travando driver UTC.

**P-transporte (GO):** normalização `norm.NFC` + ToLower + `Join(Fields)`.

**P1 (REDESENHADA):** I1 estreitada (posição de saudação, 🌙 fora de noite/madrugada, mismatch de par bom-dia/boa-tarde/boa-noite; madrugada herda noite; "descanse bem"/"até amanhã" sozinhos nunca disparam; supressão quando usuário usou a saudação — regra ESPELHE precede). I2a promessa futura (enforce; não-interrogativa; sem tool ∩ {agendar_lembrete, criar_evento}; horário sem lastro em UpcomingMedsToday ∪ ListPendingReminders). I2b claim perfectivo (audit-only; tabela claim→tool; promoção com FP<2%). I4 = substring do display OK_CRIADO quando criar_evento teve sucesso; critic LLM ELIMINADO. I3 pula quando o usuário levantou a duplicação. I5 = strip de carimbo ecoado. Rollout: `OUTPUT_GUARD_MODE=off|log|enforce` (default log), `GUARD_ENFORCE_PHONES` canário, audit `guard_violation` sempre. Aceite: 7 dias zero enforcement I1/I4.

**P2 (GO com deltas):** `fire_at` INTEGER unix epoch; índice ÚNICO PARCIAL `WHERE status='pending'`; claim atômico ANTES do envio (`UPDATE ... WHERE status='pending'`, RowsAffected==1; falha → compensação a pending, attempts cap 5 → failed); teto de staleness 2h → missed + audit; texto ecoa horário agendado; entrada `time_hhmm | offset_minutes` (espelha adiar_remedio); regra de roteamento nas 2 descrições de tool; quiet-hours: lembrete user-requested BYPASSA proactiveWindowAllowed (believe-the-user, precedente medicação).

**P3 (GO com delta):** branch aniversário retorna `"OK_CRIADO|display=" + FormatEventCreated(*created)` AGORA (não follow-up).

## Decisões nos pontos contestados

D1 carimbos por linha (sem separadores; mitigação de eco obrigatória em P0) · D2 TurnContext mínimo agora, consolidação de meds depois · D3 reescrita tool-less + cascata strip→safe-template · D4 critic I4 FORA do v1 · D5 split I2a/I2b com tabela claim→tool · D6 janela 30 por contagem mantida · D7 fuso local do usuário (travel-aware) · D8 3º subsistema confirmado; sem gate noturno em lembrete pedido · D9 guard default log, canário por phones; P0 ships sem flag.

**Ordem de ship:** P0 + P-transporte (com S2/S3) → P1 em log → enforce por aceite → P2 → P3.
