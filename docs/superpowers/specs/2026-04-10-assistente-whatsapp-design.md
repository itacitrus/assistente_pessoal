# Assistente Pessoal WhatsApp + Google Calendar

**Data:** 2026-04-10
**Status:** Aprovado
**Autor:** Giovanni + Claude

## Objetivo

Criar um assistente pessoal via WhatsApp que integra com o Google Calendar, permitindo:

- Criar compromissos na agenda a partir de áudio ou texto no WhatsApp
- Consultar a agenda por período ("como está minha agenda quinta?")
- Editar e cancelar compromissos existentes
- Receber notificações automáticas: lembrete 1h antes, resumo diário, agenda semanal

O sistema suporta múltiplos usuários, cada um com sua própria agenda Google e preferências de notificação.

## Decisões Técnicas

| Decisão | Escolha | Justificativa |
|---|---|---|
| WhatsApp | whatsmeow (Go) | Experiência prévia, lib madura, conexão WS estável |
| Transcrição | AssemblyAI via FastAPI | Reutiliza projeto existente (`transcricao_proprio`) |
| NLP / Intenção | Claude API (Anthropic) | Excelente em português, extrai JSON estruturado |
| Calendário | Google Calendar API | Requisito do projeto |
| Infra | EC2 + Docker Compose | Simples, barato (~$8-15/mês), conexão WS persistente |
| IaC | Terraform | Poucos recursos, IaC limpo |
| Banco | SQLite | Sem infra extra, suficiente para multi-user de baixo volume |

## Arquitetura

```
┌─────────────────────────────────────────────────────────────┐
│                    EC2 t3.small (Docker Compose)            │
│                                                             │
│  ┌──────────────────┐   HTTP    ┌────────────────────┐     │
│  │  whatsmeow bot   │◄────────►│  transcription-api  │     │
│  │  (Go)            │          │  (Python/FastAPI)   │     │
│  │                  │          │                     │     │
│  │ - recebe msg     │          │ - POST /transcribe  │     │
│  │ - envia msg      │          │ - AssemblyAI SDK    │     │
│  │ - scheduler      │          │ - opus/ogg input    │     │
│  │ - orquestração   │          └────────────────────┘     │
│  │ - multi-user     │                                      │
│  └────────┬─────────┘                                      │
│           │                                                 │
│     ┌─────┴──────┐                                         │
│     │  SQLite DB  │                                        │
│     │ - sessão WA │                                        │
│     │ - users     │                                        │
│     │ - pending   │                                        │
│     └────────────┘                                         │
└────────────┬───────────────────────────────────────────────┘
             │ HTTPS
     ┌───────┴────────┐
     │                │
┌────▼─────┐   ┌─────▼──────┐
│ Claude   │   │ Google     │
│ API      │   │ Calendar   │
│          │   │ API        │
│ interpreta│   │ CRUD       │
│ intenção │   │ eventos    │
└──────────┘   └────────────┘
```

## Fluxos

### Fluxo 1: Áudio/Texto para Evento

```
Usuário envia áudio ou texto no WhatsApp
    │
    ├── [áudio] ──► transcription-api (POST /transcribe) ──► texto
    ├── [texto] ──► texto direto
    │
    ▼
Claude API (interpreta intenção + extrai dados estruturados)
    │
    ▼
Retorna JSON: { intent, data, confirmation_message }
    │
    ├── intent: criar_evento
    │     ──► Bot envia confirmação ao usuário
    │     ──► Salva em pending_confirmations (status: pending)
    │     ──► Aguarda resposta:
    │           ├── "sim/ok/beleza"   ──► Cria evento no Google Calendar
    │           ├── "não/cancela"     ──► Descarta
    │           └── 2h sem resposta   ──► Auto-confirma + cria evento
    │                                      + notifica: "Confirmei automaticamente: X ✓"
    │
    ├── intent: consultar_agenda
    │     ──► Busca eventos no Google Calendar pelo período
    │     ──► Formata e envia lista no WhatsApp
    │
    ├── intent: editar_evento
    │     ──► Identifica evento, aplica alteração, confirma
    │
    └── intent: cancelar_evento
          ──► Identifica evento, remove, confirma
```

### Fluxo 2: Notificações (Scheduler)

O scheduler (`robfig/cron`) roda no processo Go e itera sobre todos os usuários ativos:

| Notificação | Frequência | Comportamento |
|---|---|---|
| Lembrete | A cada minuto (verifica eventos próximos) | Envia mensagem 1h antes do evento (configurável por usuário) |
| Resumo diário | Configurável por usuário (default: 07:00) | Lista eventos do dia |
| Agenda semanal | Configurável por usuário (default: domingo 20:00) | Lista eventos da semana seguinte |

O scheduler também verifica `pending_confirmations` com mais de 2h e auto-confirma.

### Fluxo 3: Mensagem de Número Desconhecido

```
Número não cadastrado envia mensagem
    ──► Bot responde: "Não te conheço ainda. Peça ao administrador para te cadastrar."
    ──► Ignora mensagens subsequentes desse número
```

## Multi-Usuário

### Modelo de Dados

```sql
CREATE TABLE users (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    phone_number        TEXT UNIQUE NOT NULL,     -- formato: 5511999999999
    name                TEXT NOT NULL,
    google_calendar_id  TEXT NOT NULL,            -- email do calendário
    google_credentials  TEXT NOT NULL,            -- refresh token (encriptado)
    daily_summary_time  TEXT DEFAULT '07:00',
    weekly_summary_day  TEXT DEFAULT 'sunday',
    weekly_summary_time TEXT DEFAULT '20:00',
    reminder_before     TEXT DEFAULT '1h',
    auto_confirm_timeout TEXT DEFAULT '2h',
    is_active           INTEGER DEFAULT 1,
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE pending_confirmations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id),
    event_data  TEXT NOT NULL,                    -- JSON com dados do evento
    status      TEXT DEFAULT 'pending',           -- pending | confirmed | cancelled
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE calendar_permissions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    grantor_id   INTEGER NOT NULL REFERENCES users(id),  -- dono da agenda (quem concede)
    grantee_id   INTEGER NOT NULL REFERENCES users(id),  -- quem recebe permissão
    created_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(grantor_id, grantee_id)
);

CREATE TABLE action_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL REFERENCES users(id),   -- quem executou
    action      TEXT NOT NULL,                            -- criar_evento, editar_evento, cancelar_evento, consultar_agenda, auto_confirm, grant_access, revoke_access
    target_user TEXT,                                     -- nome do usuario alvo (se cross-user)
    details     TEXT NOT NULL,                            -- JSON com dados completos da ação
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### Cadastro de Usuários

Via CLI no binário Go:

```bash
./bot add-user --phone=5511999999999 --name="Waldyr" --calendar=waldyr@gmail.com
```

O bot envia um link OAuth para o usuário via WhatsApp. O usuário autoriza acesso à sua agenda, e o callback salva o refresh token no SQLite.

### Isolamento

- Cada usuário tem suas próprias credenciais Google Calendar
- Confirmações pendentes vinculadas ao `user_id`
- Scheduler respeita horários individuais de cada usuário
- Prompts do Claude incluem o nome do usuário para respostas personalizadas

### Delegação de Agenda (Cross-User Permissions)

Usuários podem autorizar outros usuários a criar eventos em suas agendas. A permissão é unidirecional: se Andre autoriza Waldyr, Waldyr pode agendar na agenda do Andre, mas Andre não pode agendar na agenda do Waldyr (a menos que Waldyr também autorize Andre).

**Fluxo:**

```
Waldyr envia: "marca reunião na agenda do Andre amanhã às 10h"
    │
    ▼
Claude extrai: intent=criar_evento, target_user="Andre"
    │
    ▼
Bot verifica: Waldyr tem permissão na agenda do Andre? (calendar_permissions)
    ├── Sim ──► Cria evento na agenda do Andre E na agenda do Waldyr (ambos participam)
    └── Não ──► "Voce nao tem permissao para agendar na agenda do Andre."
```

**Gestão via CLI:**

```bash
./bot grant-access --from=5511111111111 --to=5522222222222   # Andre autoriza Waldyr
./bot revoke-access --from=5511111111111 --to=5522222222222  # Andre revoga
./bot list-access --user=5522222222222                       # Lista quem Waldyr pode agendar
```

### Audit Log

Todas as ações do sistema são registradas na tabela `action_log` com: quem executou, o que fez, o alvo (se cross-user), e os dados completos em JSON.

**Ações logadas:** `criar_evento`, `editar_evento`, `cancelar_evento`, `consultar_agenda`, `confirmar`, `negar`, `auto_confirm`, `grant_access`, `revoke_access`

**Consulta via WhatsApp:** O usuário pode perguntar "o que aconteceu na minha agenda essa semana?" e o bot retorna o histórico de ações recentes. O Claude interpreta como intent `consultar_log` com período.

## Produção e Resiliência

### Monitoramento e Recuperação

| Componente | Solução |
|---|---|
| Process supervision | Systemd unit reinicia Docker Compose em reboot/crash |
| Monitoramento | CloudWatch Agent: CPU, memória, disco + alarmes |
| Alertas | SNS para email do admin quando bot cai ou disco > 80% |
| Backup | Cron diário: SQLite backup para S3 |
| Log rotation | Docker log driver com max-size 10MB, max-file 5 |
| Auto-recovery | EC2 Auto Recovery em caso de falha de hardware |
| WhatsApp watchdog | Goroutine que verifica conexão WS a cada 5min, reconecta e alerta se falhar |

### Disponibilidade Esperada

~99.5%+ com single instance. Para >99.9% seria necessário multi-AZ (fora do escopo atual).

## Componentes

### Bot WhatsApp (Go)

**Responsabilidades:** Conexão WhatsApp, roteamento de mensagens, orquestração do pipeline, scheduler, gerenciamento de confirmações, multi-user.

**Bibliotecas:**

| Biblioteca | Uso |
|---|---|
| `go.mau.fi/whatsmeow` | Conexão e API do WhatsApp |
| `robfig/cron/v3` | Agendamento de notificações |
| `google.golang.org/api/calendar/v3` | Google Calendar CRUD |
| `github.com/liushuangls/go-anthropic/v2` | Claude API client |
| `modernc.org/sqlite` | SQLite pure Go (sem CGO) |

**Arquivos:**

| Arquivo | Responsabilidade |
|---|---|
| `main.go` | Entry point, inicializa whatsmeow + scheduler + HTTP OAuth callback |
| `handler.go` | Roteamento: detecta tipo (áudio/texto), busca user, delega |
| `orchestrator.go` | Pipeline: transcrição → Claude → ação → resposta |
| `calendar.go` | Client Google Calendar (CRUD, consulta por período) |
| `claude.go` | Client Claude API, prompts de intenção, parsing de resposta |
| `scheduler.go` | Cron jobs: lembretes, resumo diário, agenda semanal |
| `confirmation.go` | Estado de confirmações pendentes, auto-confirm após timeout |
| `users.go` | CRUD de usuários, CLI add-user, OAuth flow |
| `permissions.go` | Delegação de agenda: grant/revoke/check cross-user access |
| `audit.go` | Audit log: registrar e consultar ações |
| `watchdog.go` | Monitoramento da conexão WhatsApp |
| `config.go` | Configuração via env vars |

### Transcription API (Python/FastAPI)

**Responsabilidade:** Endpoint HTTP para transcrição de áudio.

Reutiliza o `AssemblyAIProvider` do projeto `transcricao_proprio`.

```
POST /transcribe
Content-Type: multipart/form-data
Body: file=<áudio opus/ogg/m4a>

Response 200:
{
    "text": "marcar reunião com João amanhã às 15h"
}
```

**Arquivos:**

| Arquivo | Responsabilidade |
|---|---|
| `main.py` | FastAPI app, endpoint `/transcribe`, chama AssemblyAI |
| `requirements.txt` | fastapi, uvicorn, assemblyai, python-multipart |
| `Dockerfile` | Imagem Python slim |

### Claude API — Prompt de Intenção

O prompt enviado ao Claude segue este formato:

```
Você é um assistente de agenda. Analise a mensagem do usuário {nome}
e retorne APENAS um JSON com a estrutura abaixo.

Data/hora atual: {now}

Intenções possíveis:
- criar_evento: extraia title, date (YYYY-MM-DD), time (HH:MM), duration_minutes (default: 60). Se o usuario mencionar a agenda de outra pessoa, extraia target_user com o nome da pessoa.
- consultar_agenda: extraia start_date, end_date
- editar_evento: extraia search_query (para encontrar o evento), campos a alterar
- cancelar_evento: extraia search_query
- confirmar: o usuário está confirmando uma ação pendente
- negar: o usuário está negando uma ação pendente
- consultar_log: o usuario quer ver o historico de acoes. Extraia start_date, end_date.

Responda APENAS com JSON:
{
    "intent": "...",
    "data": { ... },
    "confirmation_message": "mensagem amigável para o usuário"
}
```

## Estrutura do Repositório

```
assistente_pessoal/
├── bot/                          # Serviço Go
│   ├── main.go
│   ├── handler.go
│   ├── orchestrator.go
│   ├── calendar.go
│   ├── claude.go
│   ├── scheduler.go
│   ├── confirmation.go
│   ├── users.go
│   ├── config.go
│   ├── Dockerfile
│   ├── go.mod
│   └── go.sum
│
├── transcription/                # Serviço Python
│   ├── main.py
│   ├── requirements.txt
│   └── Dockerfile
│
├── terraform/                    # IaC AWS
│   ├── main.tf                   # EC2, SG, EIP, Key Pair
│   ├── variables.tf
│   ├── outputs.tf
│   └── cloud-init.yaml           # Bootstrap: Docker, docker-compose, clone repo
│
├── docker-compose.yml            # Orquestra bot + transcription
├── .env.example
└── docs/
    └── superpowers/
        └── specs/
            └── 2026-04-10-assistente-whatsapp-design.md
```

## Variáveis de Ambiente

```env
# WhatsApp
AUTHORIZED_NUMBERS=5511999999999   # números iniciais (depois gerenciado via DB)

# Google Calendar (OAuth app credentials — não per-user)
GOOGLE_CLIENT_ID=...
GOOGLE_CLIENT_SECRET=...
GOOGLE_REDIRECT_URI=http://<ec2-ip>:8080/oauth/callback

# Claude
ANTHROPIC_API_KEY=sk-ant-...

# Transcrição
ASSEMBLYAI_API_KEY=...

# Encryption (para refresh tokens no SQLite)
ENCRYPTION_KEY=...

# Scheduler defaults (override por usuário no DB)
DEFAULT_DAILY_SUMMARY_TIME=07:00
DEFAULT_WEEKLY_SUMMARY_DAY=sunday
DEFAULT_WEEKLY_SUMMARY_TIME=20:00
DEFAULT_REMINDER_BEFORE=1h
DEFAULT_AUTO_CONFIRM_TIMEOUT=2h
```

## Infraestrutura (Terraform)

**Recursos AWS:**

| Recurso | Spec | Justificativa |
|---|---|---|
| EC2 | `t3.small` (2 vCPU, 2GB RAM) | Confortável para Go + Python + SQLite |
| Security Group | 22/SSH (restrito ao IP do admin) | Sem portas expostas desnecessárias |
| Elastic IP | 1 | IP fixo — sessão WhatsApp não se perde em reboot |
| EBS | 20GB gp3 | Suficiente para DB + áudios temporários |

**Cloud-init:** Instala Docker + Docker Compose, clona o repo, copia `.env`, executa `docker compose up -d`.

## Segurança

- Refresh tokens do Google encriptados no SQLite (AES-256-GCM)
- Apenas números cadastrados podem interagir com o bot
- SSH restrito ao IP do administrador
- API keys em variáveis de ambiente (nunca no código)
- Comunicação entre containers via rede Docker interna (não exposta)
- OAuth callback na porta 8080, restrito ao Security Group (acessível apenas durante onboarding via SSH tunnel: `ssh -L 8080:localhost:8080 ec2-user@<ip>`)

## Custos Estimados

| Item | Custo mensal |
|---|---|
| EC2 t3.small | ~$15 |
| AssemblyAI | ~$0.02/min de áudio |
| Claude API | ~$0.01-0.05/request |
| Google Calendar API | Gratuito |
| Elastic IP | Gratuito (enquanto associado) |
| **Total estimado** | **~$15-20/mês** (uso leve) |
