# Plano Geral — Lurch como Assistente de Idosos

**Data:** 2026-05-09
**Autor:** Giovanni (planejado com Claude)
**Status:** Planejamento — aguardando aprovação por fase

---

## 1. Problema e público-alvo

O Lurch hoje é um assistente pessoal via WhatsApp focado em agenda (Google Calendar). Esta feature expande o produto pra **acompanhamento de idosos** e seus familiares, com três grandes capacidades novas:

1. **Lembrete e confirmação ativa de medicamentos** — Lurch insiste até confirmar, e escala pra família se o idoso não responder.
2. **Companion psicológico** — Lurch como amigo acolhedor, com memória social do idoso, iniciando conversa proativamente.
3. **Painel/relatório do responsável** — familiar cadastrado consegue ver status do idoso e recebe alertas reativos.

A feature deve **estender** o produto, não bifurcar. O mesmo bot, mesmo número, mesma orquestração — o que muda é tipo de usuário, persona, tools e jobs do scheduler.

### Personas

| Persona              | Tipo no sistema     | Interage por      | Caso típico                                                    |
| -------------------- | ------------------- | ----------------- | -------------------------------------------------------------- |
| Usuário comum        | `comum`             | WhatsApp          | "marcar reunião amanhã 15h" (uso atual)                        |
| Idoso monitorado     | `idoso`             | WhatsApp          | "tomei o remédio", "minha filha não me liga há dias"           |
| Responsável familiar | `responsavel`       | WhatsApp + Web UI | "como está minha mãe?", recebe alerta de remédio não tomado    |

Tipos não são exclusivos — um responsável também é usuário comum (tem agenda própria).

---

## 2. Arquitetura de alto nível

### 2.1 Decisões arquiteturais

**D1. Persona dinâmica por tipo de usuário, não múltiplos agentes.**
O `Agent.Run()` continua único. O system prompt é montado dinamicamente baseado no `user.Type` (já existe esse padrão pra `pending permission requests` em `agent.go:445-457`). Persona "idoso" ganha system prompt distinto — acolhedora, com base em escuta ativa e CBT light, com disclaimer claro de não-substituir-terapeuta.
**Alternativa rejeitada:** segundo agente paralelo. Adicionaria complexidade de roteamento sem benefício — Claude consegue trocar de tom via prompt.

**D2. Sub-agente apenas pra síntese psicológica (read-only, Haiku).**
Quando o responsável pergunta "como está minha mãe?", um sub-agente Haiku **separado** recebe as últimas N conversas do idoso e produz parecer breve. Justificativa: prompt diferente (psicológico/observacional vs. operacional), Haiku é ~1/10 do custo, e protege o contexto principal de carregar transcrições. Padrão isolado: input claro (transcrições + medicação), output estruturado (JSON com `humor`, `alertas`, `resumo`).

**D3. Lembretes de remédio reusam `pending_confirmations`, com campo `kind` discriminador.**
A tabela já é polimórfica via `event_data` JSON. Adicionamos coluna `kind` (`event` | `medication`) e estendemos os handlers de confirmação. Não duplicamos infraestrutura.

**D4. Escalação como motor genérico, não específica de remédio.**
Nova abstração `EscalationPolicy` (campos: `attempts`, `interval`, `escalate_to`). Aplica-se a remédio agora; serve futuramente pra qualquer pending crítico (ex: confirmação de viagem urgente). A política é dado, não código.

**D5. `Notifier` interface — WhatsApp impl única no MVP, Twilio futuro.**
`type Notifier interface { Send(ctx, recipient, message) error }`. Hoje só `WhatsAppNotifier`. Quando voz entrar, vira `TwilioVoiceNotifier`. Camada de escalação chama o `Notifier` apropriado por canal — não conhece detalhes de transporte.

**D6. UI separada em `/web` (Next.js) consumindo API REST nova no bot Go.**
Deploy independente (Vercel), autenticação web própria (magic link via WhatsApp — Lurch envia link de login com token de uso único). Bot ganha endpoints REST `/api/v1/*` com middleware de auth (token bearer ou cookie de sessão).

**D7. Mensagens proativas seguem o padrão atual do scheduler (cron 1-min + idempotência via DB).**
Novos jobs: `checkMedicationReminders`, `checkMedicationEscalation`, `checkInactivity`, `checkInactivityEscalation`, `runDailyPsychSnapshot`. Cada um marca seu lock (tabela `*_sent` ou status na pending) pra sobreviver a restart sem duplicar.

**D8. Estratégia de modelos 3-tier (cost × sensitivity × volume).**
Cada papel tem o modelo mais adequado ao seu trade-off, atrás de uma abstração `ChatProvider`/`AnalysisProvider`/`ReportProvider` em Go (interface trivial, impls trocáveis):

| Papel                                          | Modelo            | Justificativa                                                          |
| ---------------------------------------------- | ----------------- | ---------------------------------------------------------------------- |
| Charles Lurch operacional (`comum`/`responsavel`) | **Sonnet 4.6/4.7** | inalterado — agente principal com tools complexas, qualidade-crítica.  |
| Companion / chat com idoso                      | **DeepSeek V4-Flash** | volume alto (3-10 turnos/dia/idoso), cost-sensitive, baixa carga clínica por turno. |
| Snapshot psicológico diário + safety review     | **Haiku 4.5**     | analisa conversa do dia, atualiza estado psicológico, **revisa o que o companion possa ter deixado passar** (safety net pra ideação suicida, queda, etc). |
| Síntese final pro responsável (`status_dependente`) | **Sonnet 4.6/4.7** | output sensível, baixo volume, alta carga ética e nuance de linguagem. |
| Vision (foto da receita, foto enviada pelo idoso) | **Haiku 4.5 vision** | provado em pt-BR/manuscrito médico/cenas familiares; DeepSeek vision é menos validado. |

**Trade-off documentado conscientemente:**
- DeepSeek roda em infra hospedada na China. Conversa do idoso passa por ela. Mitigações:
  - Termo de consentimento explícito do idoso E do responsável no onboarding (Fase 2) cita o uso de provider externo.
  - Sem PII estruturada na conversa (nome só, sem CPF/endereço/saúde detalhada).
  - Memórias `social_context` (que ficam na nossa SQLite) **não** são sincronizadas com DeepSeek — são contexto efêmero por turno.
  - **Safety net Haiku**: cada conversa significativa é revisada pelo snapshot writer (Haiku, mais conservador) que pode disparar `alertar_familia` se o companion DeepSeek deixou passar sinal sério. Custo ~$0.001/conversa; vale.
- Caminho de revisão: se LGPD/regulador apontar problema, troca-se DeepSeek por Haiku no companion via flag de provider — interface preserva.

**D9. Mídia rica como cidadã de primeira classe no companion.**
Idoso recebe muita coisa em grupos de WhatsApp (foto da família, meme, link de notícia, vídeo do TikTok). O companion engaja com isso — não ignora nem responde "não entendi":

| Tipo de mídia | Tratamento                                                                                              |
| ------------- | ------------------------------------------------------------------------------------------------------- |
| **Áudio**     | Já existe — `transcription/` (AssemblyAI) transcreve; companion responde ao texto.                     |
| **Imagem**    | Tool nova `comentar_imagem(image_id)` — chama Haiku 4.5 vision pra descrever, e devolve descrição+sugestão de tom; o companion (DeepSeek) incorpora numa resposta natural ("ah, que linda essa foto da família!"). Caching: imagem armazenada efêmera 24h, não persistida. |
| **Link**      | Tool nova `comentar_link(url)` — domain allowlist (Instagram, Facebook, YouTube, TikTok, X/Twitter, news majors), busca apenas Open Graph metadata (`og:title`, `og:description`, `og:image`), sem fetch de HTML cru. Tamanho máximo 8KB, timeout 3s, sem follow de redirects pra domínios fora da allowlist. Mitigates SSRF/exfiltração. |
| **Sticker/GIF** | Trata como imagem — mesmo fluxo `comentar_imagem`.                                                  |
| **Vídeo**     | Por enquanto: bot reconhece e responde "vi que você mandou um vídeo, me conta do que se trata?". Análise de vídeo entra em fase futura.                                              |

Idoso pode mandar imagem só pra mostrar — companion comenta com leveza, não sai analisando exaustivamente. O snapshot writer (Haiku) também recebe descrições agregadas das imagens/links (não as imagens cruas) pra montar contexto do dia.

### 2.2 Mapa de mudanças no código

| Camada                  | O que muda                                                                    |
| ----------------------- | ----------------------------------------------------------------------------- |
| **Schema SQLite**       | +`user_type`, +`family_links`, +`medications`, +`medication_schedules`, +`medication_intake_log`, +`escalations`, +`proactive_attempts`, +`psych_state_daily`, +`web_sessions`. Modificações em `pending_confirmations` (`kind`) e `users` (`last_user_message_at`, `inactivity_threshold_hours`, `proactive_paused_until`). |
| **Tools (Claude)**      | +`cadastrar_medicamento`, +`listar_medicamentos`, +`marcar_remedio_tomado`, +`pular_dose`, +`registrar_familia`, +`status_dependente`, +`alertar_familia`, +`pausar_proatividade`, +`comentar_imagem`, +`comentar_link`. Tools existentes inalteradas. |
| **System prompt**       | Switch por `user.Type`. Persona idoso em arquivo separado (`prompts/companion.go`).                  |
| **Scheduler**           | 5 jobs novos (medicamento × 2 + inatividade × 2 + snapshot diário), todos cron 1-min com lock em DB.   |
| **HTTP server**         | Bot Go ganha mais endpoints `/api/v1/*` além do `/oauth/callback` atual. Middleware de auth novo.     |
| **Provider abstraction** | Interfaces `ChatProvider`, `AnalysisProvider`, `ReportProvider`, `VisionProvider` no `bot/llm/` — impls: `AnthropicChat`, `AnthropicAnalysis`, `AnthropicReport`, `AnthropicVision`, `DeepSeekChat`. Permite trocar modelo por papel sem reescrever caller. |
| **Sub-agente**          | `pkg/synthesis` — pacote isolado, função pura. Versão analise-diária (Haiku) + versão síntese-relatório (Sonnet). |
| **Web (novo)**          | App Next.js em `/web`. Componentes brasileiros reusáveis (`<PhoneInput>`, `<CepInput>`, `<CurrencyInput>` se aplicável). |
| **Notifier abstration** | Refactor leve — wrapping do `sendMsg` callback atual em `Notifier` interface.                          |
| **Mídia**               | Suporte a imagem (vision via Haiku) e link (Open Graph + allowlist) no companion. Áudio segue via `transcription/` existente. |

### 2.3 O que **não** muda

- Identidade por `phone_number` continua sendo a chave primária funcional.
- `calendar_permissions` segue como mecanismo peer-to-peer pra agenda. `family_links` é camada paralela (não substitui).
- Encryption key, fluxo OAuth Google, transcription service: zero mudança.
- Tools de calendário e memória: zero mudança.

---

## 3. Os 5 pilares (escopo por fase)

### Fase 1 — Modelagem de usuários e família (`01-usuarios-familia.md`)

**Objetivo:** Criar fundação de tipos de usuário e relacionamento familiar, sem mudar comportamento existente.

**Inclui:**
- Migration: `users.type` (default `comum`), `users.last_user_message_at`.
- Tabela `family_links(id, guardian_id, dependent_id, relationship, notify_on_medication_miss, notify_on_inactivity, notify_on_severe_signal, created_at)`.
- Helpers no DB: `GetDependents(guardianID)`, `GetGuardians(dependentID)`, `LinkFamily(...)`.
- Auditoria: novas ações em `action_log` (`family_link_created`, `family_link_removed`).
- Testes: criação, listagem, deleção, integridade referencial.

**Não inclui:** UI nem tools — Fase 2/3 consomem essa fundação.

---

### Fase 2 — UI de cadastro/onboarding (`02-ui-cadastro.md`)

**Objetivo:** App web Next.js em `/web` que permita auto-cadastro de família e configuração sem CLI. Integra com endpoints REST novos no bot.

**Inclui:**
- App Next.js standalone (`/web`): landing, signup, login (magic link via WhatsApp), dashboard.
- Telas: cadastrar família → adicionar dependente (idoso) → conectar Google Calendar (opcional pro idoso) → configurar preferências (timezone, horários, alertas).
- Componentes brasileiros reusáveis: `<PhoneInput>` (máscara `(XX) XXXXX-XXXX`), `<CepInput>` (máscara + ViaCEP autocomplete). Persistência salva só dígitos.
- API REST no bot Go: `POST /api/v1/auth/request-link` (envia magic link via WhatsApp), `POST /api/v1/auth/verify`, `GET /api/v1/me`, `POST /api/v1/family/dependent`, `GET /api/v1/family/dependents`, `PATCH /api/v1/users/{id}/preferences`.
- Auth: token de sessão (httpOnly cookie + bearer pra fetch). Magic link expira em 15min.
- Deploy: Vercel pro web, bot continua na infra atual.

**Não inclui:** dashboard de status do dependente (vem na Fase 5), upload de receita (Fase 3).

---

### Fase 3 — Medicamentos (`03-medicamentos.md`)

**Objetivo:** Cadastro, lembrete e confirmação ativa de medicamentos com escalação pra família.

**Inclui:**
- Schema: `medications`, `medication_schedules` (RRULE iCal), `medication_intake_log`, `escalations`.
- Cadastro por 3 caminhos: (a) foto da receita via WhatsApp → Claude vision extrai itens estruturados → tool `cadastrar_medicamento` confirma com idoso/responsável; (b) conversa em texto; (c) UI web (Fase 2).
- Tools novas: `cadastrar_medicamento`, `listar_medicamentos`, `editar_medicamento`, `cancelar_medicamento`, `marcar_remedio_tomado`, `pular_dose`.
- Scheduler novo: `checkMedicationReminders` (dispara lembrete na hora exata, cria `pending_confirmation` kind=medication com timeout 15min), `checkMedicationEscalation` (3 retries a cada 5min; depois aciona `family_links` com `notify_on_medication_miss=true`).
- `EscalationPolicy` reusável.
- Auditoria: cada lembrete enviado, cada dose tomada/pulada/escalada.

**Não inclui:** ligação telefônica (Fase 6+), análise de adesão histórica longa (relatório vem na Fase 5).

---

### Fase 4 — Companion (persona acolhedora + proatividade) (`04-companion.md`)

**Objetivo:** Lurch versão "amigo psicólogo" pra `user.Type=idoso`, com memória social e iniciativa de conversa.

**Inclui:**
- System prompt novo `prompts/companion.go` — persona acolhedora, escuta ativa, validação emocional, **disclaimer explícito de não-substituir-profissional** e protocolo claro pra ideação suicida ou sinais de risco (= disparo imediato de `alertar_familia` com severidade alta + recomenda CVV 188).
- Switch no `buildSystemPromptStable` baseado em `user.Type`.
- Memória social: estende `user_memories` com categoria `social_context`. Lurch é instruído a salvar nomes, eventos, rotinas, "fofocas" — e reusar em futuras conversas.
- Atualização de `users.last_user_message_at` em todo handler de mensagem.
- Scheduler novo: `checkInactivity` — se idoso não fala há `inactivity_threshold_hours` (configurável, default 24h), Lurch puxa conversa **referenciando algo de `user_memories`** ("e a Dona Marta, foi pra consulta?"). Lock pra não disparar várias vezes.
- Tool nova `alertar_familia(severity, reason)` — chamada pelo Claude quando detecta sinal sério. Severidades: `info` | `warn` | `critical`.

**Não inclui:** análise psicológica agregada (sub-agente vem na Fase 5), avaliação clínica formal (fora de escopo permanente).

---

### Fase 5 — Relatório e alertas pro responsável (`05-relatorio-alertas.md`)

**Objetivo:** Responsável vê estado do dependente sob demanda e recebe alertas reativos.

**Inclui:**
- Tool nova `status_dependente(dependent_id)` — disponível **apenas** se `family_links` autoriza. Retorna agregado: aderência medicação 7d (X/Y doses), última conversa há quanto tempo, última fofoca/contexto, alertas abertos.
- Sub-agente Haiku em `pkg/synthesis/` — pacote isolado:
  - **Input:** últimas 30 mensagens do idoso + log de medicação 7d + memórias social_context.
  - **Output JSON:** `{humor: string, sinais_observados: [...], resumo: string, recomendacoes_carinhosas: [...]}`.
  - System prompt observacional — não diagnostica, só observa padrões.
- Scheduler novo: `checkInactivityEscalation` — se idoso não responde a tentativa de conversa do Lurch por mais N horas, alerta família.
- Push de alerta: usa `Notifier` (WhatsApp no MVP) + grava `escalations` row + opcional push no app web (futuro).
- Tela web no dashboard: lista de dependentes → click → status agregado + histórico de alertas.

**Não inclui:** Twilio (Fase 6+), exportação PDF (futuro).

---

## 4. Sequência de execução

```
Fase 1 (Modelagem família)        ─┐
                                   ├──► Fase 3 (Medicamentos) ─┐
Fase 2 (UI cadastro)              ─┘                            ├──► Fase 5 (Relatório)
                                                                │
Fase 4 (Companion + proatividade) ─────────────────────────────┘
```

**Por que essa ordem:**
- Fase 1 destrava todas as outras (sem `family_links`, escalação Fase 3 e relatório Fase 5 não existem).
- Fase 2 pode acontecer em paralelo com Fase 1, mas só faz sentido testar end-to-end depois de Fase 1 mergeada.
- Fase 3 e Fase 4 são independentes entre si — podem ser feitas em paralelo por desenvolvedores diferentes.
- Fase 5 depende de Fase 3 (medicação log) e Fase 4 (memória social, sinal sério).

**Critério de "pronto pra próxima fase":** testes da fase verde, deploy em prod com 1 família piloto, 1 semana de uso real sem regressão crítica.

---

## 5. Riscos e mitigações

| Risco                                                              | Probabilidade | Mitigação                                                                                       |
| ------------------------------------------------------------------ | ------------- | ----------------------------------------------------------------------------------------------- |
| **Persona acolhedora vira "terapeuta" e dá conselho clínico**      | Alta          | System prompt veta diagnóstico; tool `alertar_familia` é o único caminho pra sinais sérios; disclaimer toda vez que assunto vira saúde mental. |
| **Idoso confunde "tomei remédio" com "vou tomar" e marca antes**   | Média         | Bot pergunta confirmação só **depois** do horário, não antes. Distinção lexical clara nos prompts. |
| **Foto de receita mal-extraída cria medicamento errado**           | Alta          | SEMPRE confirmar item-a-item antes de salvar. Nunca cadastrar sem `pending_confirmation`. Logar imagem + extração no audit.  |
| **Escalação dispara excessivamente (idoso só esqueceu o celular)** | Média         | Janela 15min + 3 retries 5min = 30min total antes de escalar. Configurável por medicamento (crítico vs. não-crítico). |
| **Web auth fraca expõe dados sensíveis**                           | Alta          | Magic link via WhatsApp (canal autenticado), token de uso único 15min, sessão httpOnly + SameSite=strict, rate limit. |
| **Conversation history vaza pro responsável demais**               | Alta          | Sub-agente synthesis NUNCA retorna citações literais — só agregados. Idoso vê política no onboarding. |
| **Idoso não consegue usar smartphone bem**                         | Alta          | Lurch tolera mensagens curtas/erradas/em áudio (transcription já existe), responde simples, nunca usa menus numerados. |
| **LGPD: dados de saúde de pessoa vulnerável**                      | Crítica       | Termo de consentimento explícito do idoso E do responsável no onboarding (Fase 2). Direito de excluir tudo. Encryption at rest (já existe pra creds, estender). |

---

## 6. Fora de escopo (explícito)

- **Diagnóstico médico** ou recomendação de medicamento. Lurch nunca sugere "tome paracetamol".
- **Atendimento de emergência médica.** Bot detecta e escala — não substitui SAMU/192.
- **Voz/ligação telefônica** — Fase 6+, com `Notifier` abstration já preparada.
- **Detecção de queda via sensor** — fora de escopo permanente (não é wearable).
- **Análise clínica formal** — sub-agente é observacional, não diagnóstico.
- **Multi-idioma** — pt-BR only no MVP (já é a realidade atual).

---

## 7. Métricas de sucesso (pra avaliar pós-MVP)

- **Adesão a medicação:** % de doses confirmadas vs. agendadas, por idoso, 30d.
- **Engajamento companion:** dias/semana com pelo menos 1 conversa iniciada pelo idoso (não só responder ao Lurch).
- **Tempo até resposta em escalação:** mediana entre alerta enviado ao responsável e ação tomada.
- **Falsos positivos de sinal sério:** quantos `alertar_familia(severity=critical)` foram revertidos pelo responsável como falso alarme.
- **Retenção de família:** % de famílias ativas após 30/60/90 dias.

---

## 8. Próximos passos

Com este overview aprovado, gerar os 5 planos detalhados (`01-` a `05-` neste mesmo diretório), cada um com:

- Schema completo (DDL).
- Assinaturas exatas das tools/funções/endpoints.
- Casos de teste (caminho feliz + edge cases).
- Stub de prompts onde aplicável.
- Lista granular de tarefas (delegáveis a subagentes).
- Riscos específicos da fase.
- Checklist de "pronto".

Os planos detalhados são fonte da verdade pra implementação — este overview é o **contrato arquitetural** que eles devem respeitar.
