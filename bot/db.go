package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrUserNotFound          = errors.New("user not found")
	ErrNoPendingConfirmation = errors.New("no pending confirmation")
)

type User struct {
	ID                 int64
	PhoneNumber        string
	Name               string
	GoogleCalendarID   string
	GoogleCredentials  string
	DailySummaryTime   string
	WeeklySummaryDay   string
	WeeklySummaryTime  string
	ReminderBefore     string
	AutoConfirmTimeout string
	IsActive           bool
	CreatedAt          time.Time

	// Fase 1 (idosos): persona primaria do usuario. Default UserTypeComum
	// preserva comportamento atual pra todos os usuarios pre-existentes.
	Type UserType
	// LastUserMessageAt eh o timestamp da ultima mensagem RECEBIDA do usuario
	// (nao do bot). nil = nunca recebemos mensagem nesta versao do bot.
	// Usado por checkInactivity na Fase 4.
	LastUserMessageAt *time.Time

	// Fase 4 (idosos): horas sem mensagem do idoso antes de Lurch puxar
	// conversa. Default 24, range util 4..168 (1 semana). 0 = usar default.
	InactivityThresholdHours int
	// Fase 4: timestamp UTC ate quando proatividade esta pausada por pedido
	// do idoso ("nao me chame por 3 dias"). nil = nao pausado.
	ProactivePausedUntil *time.Time
}

type PendingConfirmation struct {
	ID        int64
	UserID    int64
	EventData string
	Status    string
	CreatedAt time.Time
	PhoneNumber string
	UserName    string

	// Fase 3 (idosos): discriminador. "event" (default) | "medication".
	// Eventos de calendario continuam ignorando os campos abaixo. Lembretes
	// de remedio populam Kind="medication" + EscalationPolicy.
	Kind string

	// EscalationPolicy aponta pra uma chave em escalationPolicies (ex:
	// "medication_default"). NULL/vazio = sem escalacao.
	EscalationPolicy *string

	// LastAttemptAt: timestamp UTC da ultima tentativa de escalacao. NULL =
	// nenhuma ainda. O scheduler usa isto pra decidir se eh hora de tentar
	// de novo (now - last >= policy.Interval).
	LastAttemptAt *time.Time

	// AttemptNumber: contador (0..MaxAttempts). Decisao final (escalar pra
	// familia) acontece quando next > MaxAttempts.
	AttemptNumber int

	// DeferredUntil: horario que o idoso disse que vai tomar ("vou tomar mais
	// tarde, la pelas 18h40"). Usado para UM lembrete gentil naquele horario.
	// NAO move o deadline de tolerancia. NULL = sem adiamento registrado.
	DeferredUntil *time.Time
}

// validKinds limita os valores aceitos em pending_confirmations.kind.
// SQLite nao tem ALTER TABLE ADD CONSTRAINT, entao a validacao vive aqui
// em Go pra evitar drift entre banco recem-criado e banco migrado.
var validKinds = map[string]bool{"event": true, "medication": true}

// validatePendingKind retorna erro se k nao for um kind reconhecido.
// Default "" eh tratado como "event" (compat retro).
func validatePendingKind(k string) error {
	if k == "" {
		return nil
	}
	if !validKinds[k] {
		return fmt.Errorf("invalid pending_confirmations.kind: %q", k)
	}
	return nil
}

type DB struct {
	conn *sql.DB
}

func NewDB(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id                   INTEGER PRIMARY KEY AUTOINCREMENT,
		phone_number         TEXT UNIQUE NOT NULL,
		name                 TEXT NOT NULL,
		google_calendar_id   TEXT NOT NULL DEFAULT '',
		google_credentials   TEXT NOT NULL DEFAULT '',
		daily_summary_time   TEXT NOT NULL DEFAULT '07:00',
		weekly_summary_day   TEXT NOT NULL DEFAULT 'sunday',
		weekly_summary_time  TEXT NOT NULL DEFAULT '20:00',
		reminder_before      TEXT NOT NULL DEFAULT '1h',
		auto_confirm_timeout TEXT NOT NULL DEFAULT '2h',
		is_active            INTEGER NOT NULL DEFAULT 1,
		created_at           DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS pending_confirmations (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL REFERENCES users(id),
		event_data TEXT NOT NULL,
		status     TEXT NOT NULL DEFAULT 'pending',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS sent_reminders (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL REFERENCES users(id),
		event_id   TEXT NOT NULL,
		sent_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(user_id, event_id)
	);

	CREATE TABLE IF NOT EXISTS action_log (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id     INTEGER NOT NULL REFERENCES users(id),
		action      TEXT NOT NULL,
		target_user TEXT,
		details     TEXT NOT NULL,
		created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS calendar_permissions (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		grantor_id INTEGER NOT NULL REFERENCES users(id),
		grantee_id INTEGER NOT NULL REFERENCES users(id),
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(grantor_id, grantee_id)
	);

	CREATE TABLE IF NOT EXISTS pending_permission_requests (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		requester_id INTEGER NOT NULL REFERENCES users(id),
		target_id    INTEGER NOT NULL REFERENCES users(id),
		event_data   TEXT NOT NULL DEFAULT '',
		status       TEXT NOT NULL DEFAULT 'pending',
		created_at   DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS user_memories (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL REFERENCES users(id),
		category   TEXT NOT NULL,
		key        TEXT NOT NULL,
		value      TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(user_id, category, key)
	);

	CREATE TABLE IF NOT EXISTS conversation_history (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id    INTEGER NOT NULL REFERENCES users(id),
		role       TEXT NOT NULL,
		content    TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS user_travel_periods (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id       INTEGER NOT NULL REFERENCES users(id),
		start_date    TEXT NOT NULL,
		end_date      TEXT NOT NULL,
		timezone      TEXT NOT NULL,
		location_name TEXT NOT NULL DEFAULT '',
		created_at    DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_user_travel_periods_user_date
		ON user_travel_periods(user_id, start_date, end_date);

	-- Fase 1 (idosos): vinculo guardian -> dependent.
	-- guardian_id eh o responsavel (recebe alertas).
	-- dependent_id eh quem esta sob cuidado (tipicamente type='idoso',
	-- mas o schema NAO impoe — flexibilidade pra futuros casos).
	-- relationship eh livre ("filha", "esposa", "neto"). Notify flags
	-- granulares por canal de alerta, todos default true (= int 1).
	CREATE TABLE IF NOT EXISTS family_links (
		id                        INTEGER PRIMARY KEY AUTOINCREMENT,
		guardian_id               INTEGER NOT NULL REFERENCES users(id),
		dependent_id              INTEGER NOT NULL REFERENCES users(id),
		relationship              TEXT NOT NULL DEFAULT '',
		notify_on_medication_miss INTEGER NOT NULL DEFAULT 1,
		notify_on_inactivity      INTEGER NOT NULL DEFAULT 1,
		notify_on_severe_signal   INTEGER NOT NULL DEFAULT 1,
		created_at                DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(guardian_id, dependent_id),
		CHECK (guardian_id != dependent_id)
	);
	CREATE INDEX IF NOT EXISTS idx_family_links_guardian  ON family_links(guardian_id);
	CREATE INDEX IF NOT EXISTS idx_family_links_dependent ON family_links(dependent_id);

	-- Fase 3 (idosos): cadastro mestre de medicamento. Um medication tem 1..N
	-- schedules. created_by_user_id pode ser != user_id quando responsavel
	-- cadastra pra idoso.
	CREATE TABLE IF NOT EXISTS medications (
		id                  INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id             INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		name                TEXT    NOT NULL,
		dose                TEXT    NOT NULL DEFAULT '',
		instructions        TEXT    NOT NULL DEFAULT '',
		active              INTEGER NOT NULL DEFAULT 1,
		created_by_user_id  INTEGER NOT NULL REFERENCES users(id),
		created_at          DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at          DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_medications_user_active
		ON medications(user_id, active);

	-- Fase 3 (idosos): horarios em RRULE iCal. Multiplos schedules permitidos
	-- pelo mesmo medication (ex: dias e horarios distintos).
	CREATE TABLE IF NOT EXISTS medication_schedules (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		medication_id   INTEGER NOT NULL REFERENCES medications(id) ON DELETE CASCADE,
		rrule           TEXT    NOT NULL,
		start_date      TEXT    NOT NULL,
		end_date        TEXT,
		critical        INTEGER NOT NULL DEFAULT 0,
		created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_med_sched_med
		ON medication_schedules(medication_id);

	-- Fase 3 (idosos): historico de tomadas. UNIQUE(medication_id, scheduled_at)
	-- eh a chave de idempotencia do scheduler — evita duplicar lembretes
	-- em restart no mesmo segundo.
	CREATE TABLE IF NOT EXISTS medication_intake_log (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		medication_id   INTEGER NOT NULL REFERENCES medications(id) ON DELETE CASCADE,
		scheduled_at    DATETIME NOT NULL,
		status          TEXT NOT NULL CHECK(status IN ('pending','taken','skipped','missed','escalated')),
		confirmed_at    DATETIME,
		response_text   TEXT,
		created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(medication_id, scheduled_at)
	);
	CREATE INDEX IF NOT EXISTS idx_intake_med_time
		ON medication_intake_log(medication_id, scheduled_at);
	CREATE INDEX IF NOT EXISTS idx_intake_status
		ON medication_intake_log(status);

	-- Fase 3 (idosos): historico de tentativas de escalacao. Uma row por
	-- (pending_confirmation, attempt_number, recipient). UNIQUE garante que
	-- um restart pos-disparo nao duplique a tentativa.
	CREATE TABLE IF NOT EXISTS escalations (
		id                       INTEGER PRIMARY KEY AUTOINCREMENT,
		pending_confirmation_id  INTEGER NOT NULL REFERENCES pending_confirmations(id) ON DELETE CASCADE,
		policy_name              TEXT    NOT NULL,
		attempt_number           INTEGER NOT NULL,
		scheduled_for            DATETIME NOT NULL,
		status                   TEXT    NOT NULL CHECK(status IN ('pending','sent','acknowledged','escalated_to_family','failed')),
		notifier_used            TEXT    NOT NULL DEFAULT 'whatsapp',
		recipient_user_id        INTEGER NOT NULL REFERENCES users(id),
		sent_at                  DATETIME,
		created_at               DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(pending_confirmation_id, attempt_number, recipient_user_id)
	);
	CREATE INDEX IF NOT EXISTS idx_escalations_pc
		ON escalations(pending_confirmation_id);
	CREATE INDEX IF NOT EXISTS idx_escalations_status_sched
		ON escalations(status, scheduled_for);
	`
	if _, err := db.conn.Exec(schema); err != nil {
		return err
	}

	// Additive migrations. SQLite has no "ADD COLUMN IF NOT EXISTS", so we
	// ignore duplicate-column errors and let anything else bubble up.
	additive := []string{
		// calendar_event_id links a travel period to the all-day "✈️ Viagem"
		// marker event on the user's calendar, so we can delete the marker
		// when the period is canceled.
		`ALTER TABLE user_travel_periods ADD COLUMN calendar_event_id TEXT NOT NULL DEFAULT ''`,
		// reauth_notified_at tracks when the user last received an automatic
		// reauth-link message. NULL means never notified or already reauthorized.
		// Used to rate-limit the per-minute scheduler from spamming.
		`ALTER TABLE users ADD COLUMN reauth_notified_at DATETIME`,
		// Fase 1 (idosos): persona primaria do usuario.
		// CHECK ('comum'|'idoso'|'responsavel') vive em Go (ValidateUserType)
		// pra evitar divergencia entre banco recem-criado e banco migrado.
		`ALTER TABLE users ADD COLUMN type TEXT NOT NULL DEFAULT 'comum'`,
		// Fase 1 (idosos): timestamp da ultima mensagem RECEBIDA do usuario.
		// NULL = nunca recebemos mensagem do usuario nesta versao.
		`ALTER TABLE users ADD COLUMN last_user_message_at DATETIME`,

		// Fase 3 (idosos): discriminador entre evento de calendario e lembrete
		// de remedio. Default 'event' preserva semantica anterior — todas as
		// rows pre-Fase-3 sao eventos. CHECK constraint vive em validatePendingKind
		// (camada Go), porque SQLite nao suporta ADD CONSTRAINT.
		`ALTER TABLE pending_confirmations ADD COLUMN kind TEXT NOT NULL DEFAULT 'event'`,

		// Fase 3 (idosos): politica de escalacao aplicada a pending. NULL =
		// sem escalacao (default pra eventos de calendario — eles auto-confirmam
		// via timeout, nao escalam).
		`ALTER TABLE pending_confirmations ADD COLUMN escalation_policy TEXT`,

		// Fase 3 (idosos): lock de idempotencia do scheduler de escalacao. Sem
		// isto, dois ticks do cron com diferenca de 1s podem escalar duas vezes.
		`ALTER TABLE pending_confirmations ADD COLUMN last_attempt_at DATETIME`,

		// Fase 3 (idosos): contador de tentativas. Incrementado pelo motor de
		// escalacao. Default 0 = nenhuma tentativa ainda.
		`ALTER TABLE pending_confirmations ADD COLUMN attempt_number INTEGER NOT NULL DEFAULT 0`,

		// Fase 4 (idosos): companion + proatividade.
		// inactivity_threshold_hours: horas sem mensagem do idoso antes de Lurch
		// puxar conversa. Default 24, range util 4..168.
		`ALTER TABLE users ADD COLUMN inactivity_threshold_hours INTEGER NOT NULL DEFAULT 24`,
		// proactive_paused_until: timestamp UTC ate quando proatividade esta
		// pausada por pedido do idoso. NULL = nao pausado.
		`ALTER TABLE users ADD COLUMN proactive_paused_until DATETIME`,

		// Fase 4: tabela de tentativas proativas (Lurch puxa conversa).
		// status: 'sent' | 'failed' | 'replied' | 'ignored'.
		// MarkUserMessageReceived flipa 'sent' -> 'replied' quando idoso responde.
		`CREATE TABLE IF NOT EXISTS proactive_attempts (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id       INTEGER NOT NULL REFERENCES users(id),
			attempted_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			message_sent  TEXT NOT NULL DEFAULT '',
			status        TEXT NOT NULL DEFAULT 'sent',
			replied_at    DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_proactive_attempts_user_attempted
			ON proactive_attempts(user_id, attempted_at DESC)`,

		// Fase 4: colunas extras em escalations para suportar:
		//   1. severe_signal (alertar_familia) — sem pending_confirmation.
		//      Usa user_id, severity, details.
		//   2. linkagem com proactive_attempts (Fase 5 vai consumir via
		//      checkInactivityEscalation, mas a coluna nasce aqui porque
		//      proactive_attempts nasce nesta fase).
		`ALTER TABLE escalations ADD COLUMN user_id INTEGER REFERENCES users(id)`,
		`ALTER TABLE escalations ADD COLUMN severity TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE escalations ADD COLUMN details TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE escalations ADD COLUMN proactive_attempt_id INTEGER REFERENCES proactive_attempts(id)`,
		`CREATE INDEX IF NOT EXISTS idx_escalations_inactivity_lookup
			ON escalations(user_id, policy_name, proactive_attempt_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_escalations_severe_signal
			ON escalations(user_id, policy_name, severity, created_at DESC)`,

		// Fase 5 (idosos): consentimento explicito do dependente sobre
		// relatorio agregado pra responsavel. Default 'active' preserva
		// comportamento atual (todos os vinculos pre-Fase-5 ficam consultaveis).
		// Valores aceitos: 'active' | 'revoked' (validados em Go).
		`ALTER TABLE family_links ADD COLUMN dependent_consent_status TEXT NOT NULL DEFAULT 'active'`,

		// Fase 5 (idosos): snapshot longitudinal diario de estado psicologico.
		// UMA linha por (user_id, snapshot_date). Multiplos triggers no mesmo
		// dia fazem UPSERT (refinam a leitura). Confidence 1-5 indica robustez.
		// Scores 0/NULL significam "sem dado pra inferir", NAO "muito baixo".
		`CREATE TABLE IF NOT EXISTS psych_state_daily (
			id                  INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id             INTEGER NOT NULL REFERENCES users(id),
			snapshot_date       DATE NOT NULL,
			humor_score         INTEGER,
			humor_nuance        TEXT NOT NULL DEFAULT '',
			energia_score       INTEGER,
			sociabilidade_score INTEGER,
			autocuidado_score   INTEGER,
			sinais_observados   TEXT NOT NULL DEFAULT '[]',
			eventos_dia         TEXT NOT NULL DEFAULT '[]',
			n_conversations     INTEGER NOT NULL DEFAULT 0,
			n_messages          INTEGER NOT NULL DEFAULT 0,
			duration_minutes    INTEGER NOT NULL DEFAULT 0,
			confidence          INTEGER NOT NULL DEFAULT 1,
			inferred_at         DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(user_id, snapshot_date)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_psych_state_user_date
			ON psych_state_daily(user_id, snapshot_date DESC)`,

		// Fase 5 (idosos): sintese longitudinal (Sonnet) PERSISTIDA. Uma linha
		// por dependente. Servida instantaneamente na pagina do dependente —
		// a geracao (cara) acontece fora do request: regen assincrono quando
		// fica "stale" (generated_at < snapshot mais recente) e refresh diario.
		// payload eh o JSON de synthesis.ReportOutput. days registra a janela
		// usada na ultima geracao.
		`CREATE TABLE IF NOT EXISTS dependent_synthesis (
			dependent_id INTEGER PRIMARY KEY REFERENCES users(id),
			days         INTEGER NOT NULL,
			payload      TEXT NOT NULL,
			generated_at DATETIME NOT NULL
		)`,

		// Fase 2 (web/UI): insights de uso da agenda (Sonnet) PERSISTIDOS, por
		// (user_id, days). Mesma motivacao da sintese: tirar a geracao cara do
		// caminho do request — o dashboard do titular ficava lento no login.
		// Servido instantaneamente; regen assincrono quando "stale" (mais velho
		// que o TTL). payload = JSON de api.InsightsResponse.
		`CREATE TABLE IF NOT EXISTS user_agenda_insights (
			user_id      INTEGER NOT NULL REFERENCES users(id),
			days         INTEGER NOT NULL,
			payload      TEXT NOT NULL,
			generated_at DATETIME NOT NULL,
			PRIMARY KEY (user_id, days)
		)`,

		// Fase 2 (web/UI): sessoes do painel web. Token plaintext nunca eh
		// gravado — apenas sha256(token) em token_hash. status segue o ciclo
		// pending -> active -> revoked|expired. expires_at carrega:
		//   - pending: created_at + 15min (validade do magic link)
		//   - active : sliding window 30d, atualizado a cada RequireAuth
		// ip e user_agent vem do request original e ajudam auditoria. Nao
		// sao usados para "amarrar" a sessao a um device — proxies/NAT
		// trocam IP, e amarrar quebra UX legitima.
		`CREATE TABLE IF NOT EXISTS web_sessions (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id       INTEGER NOT NULL REFERENCES users(id),
			token_hash    TEXT NOT NULL UNIQUE,
			status        TEXT NOT NULL DEFAULT 'pending'
			               CHECK (status IN ('pending','active','revoked','expired')),
			expires_at    DATETIME NOT NULL,
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			activated_at  DATETIME,
			last_used_at  DATETIME,
			revoked_at    DATETIME,
			ip            TEXT NOT NULL DEFAULT '',
			user_agent    TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_web_sessions_user
			ON web_sessions(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_web_sessions_token
			ON web_sessions(token_hash)`,
		`CREATE INDEX IF NOT EXISTS idx_web_sessions_status_expires
			ON web_sessions(status, expires_at)`,

		// Fase 2 (web/UI): tentativas de login (request-link). Usado pra
		// rate limit por phone (3/h) e por IP (10/h). Hard-delete periodico
		// nao implementado — tabela cresce devagar (1 row por tentativa,
		// raro), volume alto justificaria sweep noturno.
		`CREATE TABLE IF NOT EXISTS web_login_attempts (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			phone        TEXT NOT NULL,
			ip           TEXT NOT NULL DEFAULT '',
			created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_web_login_attempts_phone_time
			ON web_login_attempts(phone, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_web_login_attempts_ip_time
			ON web_login_attempts(ip, created_at)`,

		// OAuth state opaco do fluxo de conexao com o Google Calendar.
		// Substitui o telefone-como-state (adivinhavel) por um token aleatorio
		// de uso unico, vinculado ao user alvo. Gravamos apenas sha256(token)
		// em token_hash — o plaintext vive so na URL de consentimento (no
		// navegador do titular ou na mensagem de WhatsApp do dependente).
		// used_at NULL = ainda nao consumido; o callback marca no resgate,
		// tornando o token single-use. expires_at fecha a janela de validade.
		`CREATE TABLE IF NOT EXISTS oauth_states (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id     INTEGER NOT NULL REFERENCES users(id),
			token_hash  TEXT NOT NULL UNIQUE,
			expires_at  DATETIME NOT NULL,
			created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			used_at     DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_oauth_states_expires
			ON oauth_states(expires_at)`,

		// Fase 3.1 (idosos): tolerancia e politica de dose atrasada, configuradas
		// pelo responsavel. tolerance_minutes = janela de carencia apos o horario
		// agendado antes de marcar "nao confirmada" e avisar a familia (em segredo).
		// late_dose_policy = orientacao (NAO acao automatica) que o bot passa ao
		// idoso sobre o que fazer se passar do horario. Valores validados em Go
		// (ValidateLateDosePolicy): 'consult_doctor' (default seguro = decisao do
		// medico), 'skip', 'take_keep_next', 'take_recalculate'.
		`ALTER TABLE medications ADD COLUMN tolerance_minutes INTEGER NOT NULL DEFAULT 30`,
		`ALTER TABLE medications ADD COLUMN late_dose_policy TEXT NOT NULL DEFAULT 'consult_doctor'`,

		// Fase 3.1 (idosos): horario que o idoso disse que vai tomar ("vou tomar
		// mais tarde, la pelas 18h40"). Usado para UM unico lembrete gentil no
		// horario dito (sem cobranca repetida). NAO move o deadline de tolerancia
		// — a familia continua sendo avisada em scheduled_at + tolerance_minutes
		// se nao houver confirmacao. NULL = sem adiamento registrado.
		`ALTER TABLE pending_confirmations ADD COLUMN deferred_until DATETIME`,
	}
	for _, stmt := range additive {
		if _, err := db.conn.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("additive migration %q: %w", stmt, err)
		}
	}

	// CONVENCAO: indices em colunas adicionadas via additive migration vao
	// AQUI, nao no schema base. Garante que a coluna existe antes do CREATE
	// INDEX rodar em banco antigo (em primeiro deploy pos-migration).
	postAdditive := `CREATE INDEX IF NOT EXISTS idx_users_type ON users(type);`
	if _, err := db.conn.Exec(postAdditive); err != nil {
		return fmt.Errorf("post-additive migration: %w", err)
	}
	return nil
}

func (db *DB) AddConversationMessage(userID int64, role, content string) error {
	_, err := db.conn.Exec(
		`INSERT INTO conversation_history (user_id, role, content) VALUES (?, ?, ?)`,
		userID, role, content)
	if err != nil {
		return err
	}
	// Keep only last 20 messages per user
	db.conn.Exec(`DELETE FROM conversation_history WHERE user_id = ? AND id NOT IN (
		SELECT id FROM conversation_history WHERE user_id = ? ORDER BY created_at DESC LIMIT 50
	)`, userID, userID)
	return nil
}

func (db *DB) GetConversationHistory(userID int64, limit int) ([]ConversationMessage, error) {
	rows, err := db.conn.Query(
		`SELECT role, content, created_at FROM conversation_history
		 WHERE user_id = ? ORDER BY created_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ConversationMessage
	for rows.Next() {
		var m ConversationMessage
		if err := rows.Scan(&m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	// Reverse to chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, rows.Err()
}

type ConversationMessage struct {
	Role      string
	Content   string
	CreatedAt time.Time
}

func (db *DB) SearchConversationHistory(userID int64, query string, limit int) ([]ConversationMessage, error) {
	rows, err := db.conn.Query(
		`SELECT role, content, created_at FROM conversation_history
		 WHERE user_id = ? AND content LIKE ? ORDER BY created_at DESC LIMIT ?`,
		userID, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []ConversationMessage
	for rows.Next() {
		var m ConversationMessage
		if err := rows.Scan(&m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	// Reverse to chronological order
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, rows.Err()
}

type UserMemory struct {
	Category string
	Key      string
	Value    string
}

func (db *DB) SaveMemory(userID int64, category, key, value string) error {
	_, err := db.conn.Exec(
		`INSERT INTO user_memories (user_id, category, key, value) VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_id, category, key) DO UPDATE SET value = ?, updated_at = CURRENT_TIMESTAMP`,
		userID, category, key, value, value)
	return err
}

func (db *DB) GetMemories(userID int64, category string) ([]UserMemory, error) {
	query := `SELECT category, key, value FROM user_memories WHERE user_id = ?`
	args := []any{userID}
	if category != "" {
		query += ` AND category = ?`
		args = append(args, category)
	}
	query += ` ORDER BY category, key`

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mems []UserMemory
	for rows.Next() {
		var m UserMemory
		if err := rows.Scan(&m.Category, &m.Key, &m.Value); err != nil {
			return nil, err
		}
		mems = append(mems, m)
	}
	return mems, rows.Err()
}

func (db *DB) SearchMemories(userID int64, query string) ([]UserMemory, error) {
	rows, err := db.conn.Query(
		`SELECT category, key, value FROM user_memories
		 WHERE user_id = ? AND (key LIKE ? OR value LIKE ? OR category LIKE ?)
		 ORDER BY updated_at DESC LIMIT 20`,
		userID, "%"+query+"%", "%"+query+"%", "%"+query+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mems []UserMemory
	for rows.Next() {
		var m UserMemory
		if err := rows.Scan(&m.Category, &m.Key, &m.Value); err != nil {
			return nil, err
		}
		mems = append(mems, m)
	}
	return mems, rows.Err()
}

func (db *DB) DeleteMemory(userID int64, category, key string) error {
	_, err := db.conn.Exec(
		`DELETE FROM user_memories WHERE user_id = ? AND category = ? AND key = ?`,
		userID, category, key)
	return err
}

func (db *DB) CreateUser(u *User) error {
	result, err := db.conn.Exec(
		`INSERT INTO users (phone_number, name, google_calendar_id, google_credentials,
		 daily_summary_time, weekly_summary_day, weekly_summary_time,
		 reminder_before, auto_confirm_timeout)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.PhoneNumber, u.Name, u.GoogleCalendarID, u.GoogleCredentials,
		defaultStr(u.DailySummaryTime, "07:00"),
		defaultStr(u.WeeklySummaryDay, "sunday"),
		defaultStr(u.WeeklySummaryTime, "20:00"),
		defaultStr(u.ReminderBefore, "1h"),
		defaultStr(u.AutoConfirmTimeout, "2h"),
	)
	if err != nil {
		return err
	}
	u.ID, _ = result.LastInsertId()
	return nil
}

func (db *DB) GetUserByPhone(phone string) (*User, error) {
	u := &User{}
	var ut sql.NullString
	var lastMsg sql.NullTime
	var thresh sql.NullInt64
	var paused sql.NullTime
	err := db.conn.QueryRow(
		`SELECT id, phone_number, name, google_calendar_id, google_credentials,
		 daily_summary_time, weekly_summary_day, weekly_summary_time,
		 reminder_before, auto_confirm_timeout, is_active, created_at,
		 type, last_user_message_at,
		 inactivity_threshold_hours, proactive_paused_until
		 FROM users WHERE phone_number = ?`, phone,
	).Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
		&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
		&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt,
		&ut, &lastMsg, &thresh, &paused)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err == nil {
		scanUserExtras(u, ut, lastMsg)
		scanUserPhase4(u, thresh, paused)
	}
	return u, err
}

func (db *DB) ListActiveUsers() ([]User, error) {
	rows, err := db.conn.Query(
		`SELECT id, phone_number, name, google_calendar_id, google_credentials,
		 daily_summary_time, weekly_summary_day, weekly_summary_time,
		 reminder_before, auto_confirm_timeout, is_active, created_at,
		 type, last_user_message_at,
		 inactivity_threshold_hours, proactive_paused_until
		 FROM users WHERE is_active = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		var ut sql.NullString
		var lastMsg sql.NullTime
		var thresh sql.NullInt64
		var paused sql.NullTime
		if err := rows.Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
			&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
			&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt,
			&ut, &lastMsg, &thresh, &paused); err != nil {
			return nil, err
		}
		scanUserExtras(&u, ut, lastMsg)
		scanUserPhase4(&u, thresh, paused)
		users = append(users, u)
	}
	return users, rows.Err()
}

func (db *DB) UpdateUserCredentials(userID int64, encryptedCredentials string) error {
	_, err := db.conn.Exec(
		`UPDATE users SET google_credentials = ?, reauth_notified_at = NULL WHERE id = ?`,
		encryptedCredentials, userID)
	return err
}

// UpdateUserPhone troca o telefone do usuario. A unicidade eh pre-checada pelo
// caller (apiAdapter.UpdateDependent); a constraint UNIQUE em users.phone_number
// eh o backstop — se violada, o erro borbulha encapsulado.
func (db *DB) UpdateUserPhone(userID int64, phone string) error {
	_, err := db.conn.Exec(
		`UPDATE users SET phone_number = ? WHERE id = ?`, phone, userID)
	if err != nil {
		return fmt.Errorf("update user phone: %w", err)
	}
	return nil
}

func (db *DB) GetReauthNotifiedAt(userID int64) (*time.Time, error) {
	var notifiedAt sql.NullTime
	err := db.conn.QueryRow(
		`SELECT reauth_notified_at FROM users WHERE id = ?`, userID,
	).Scan(&notifiedAt)
	if err != nil {
		return nil, err
	}
	if !notifiedAt.Valid {
		return nil, nil
	}
	t := notifiedAt.Time
	return &t, nil
}

func (db *DB) SetReauthNotifiedAt(userID int64, t time.Time) error {
	_, err := db.conn.Exec(
		`UPDATE users SET reauth_notified_at = ? WHERE id = ?`, t.UTC(), userID,
	)
	return err
}

func (db *DB) CreatePendingConfirmation(pc *PendingConfirmation) error {
	if err := validatePendingKind(pc.Kind); err != nil {
		return err
	}
	// Fase 3: cancelamos apenas pendings do MESMO kind. Antes da Fase 3,
	// um usuario tinha no maximo 1 pending (tudo era evento). Agora um idoso
	// pode ter um pending de evento de calendario E um de remedio simultaneos
	// — sao fluxos independentes. Cancelar tudo a cada novo pending quebraria
	// a escalacao do remedio quando o user tenta marcar outro evento.
	kindForFilter := pc.Kind
	if kindForFilter == "" {
		kindForFilter = "event"
	}
	db.conn.Exec(`UPDATE pending_confirmations SET status = 'cancelled'
		WHERE user_id = ? AND status = 'pending' AND kind = ?`,
		pc.UserID, kindForFilter)

	result, err := db.conn.Exec(
		`INSERT INTO pending_confirmations (user_id, event_data, kind, escalation_policy)
		 VALUES (?, ?, COALESCE(NULLIF(?, ''), 'event'), ?)`,
		pc.UserID, pc.EventData, pc.Kind, pc.EscalationPolicy)
	if err != nil {
		return err
	}
	pc.ID, _ = result.LastInsertId()
	if pc.Kind == "" {
		pc.Kind = "event"
	}
	return nil
}

func (db *DB) GetPendingConfirmation(userID int64) (*PendingConfirmation, error) {
	pc := &PendingConfirmation{}
	var kind sql.NullString
	var policy sql.NullString
	var lastAttempt sql.NullTime
	var attempt sql.NullInt64
	var deferred sql.NullTime
	err := db.conn.QueryRow(
		`SELECT id, user_id, event_data, status, created_at,
		        kind, escalation_policy, last_attempt_at, attempt_number, deferred_until
		 FROM pending_confirmations WHERE user_id = ? AND status = 'pending'
		 ORDER BY created_at DESC LIMIT 1`, userID,
	).Scan(&pc.ID, &pc.UserID, &pc.EventData, &pc.Status, &pc.CreatedAt,
		&kind, &policy, &lastAttempt, &attempt, &deferred)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPendingConfirmation
	}
	if err == nil {
		fillPendingExtras(pc, kind, policy, lastAttempt, attempt, deferred)
	}
	return pc, err
}

// fillPendingExtras hidrata os campos da Fase 3 que vivem em colunas adicionais.
// Mantido em helper pra evitar duplicacao em getters multiplos.
func fillPendingExtras(pc *PendingConfirmation, kind, policy sql.NullString, lastAttempt sql.NullTime, attempt sql.NullInt64, deferred sql.NullTime) {
	if kind.Valid && kind.String != "" {
		pc.Kind = kind.String
	} else {
		pc.Kind = "event"
	}
	if policy.Valid && policy.String != "" {
		s := policy.String
		pc.EscalationPolicy = &s
	}
	if lastAttempt.Valid {
		t := lastAttempt.Time
		pc.LastAttemptAt = &t
	}
	if attempt.Valid {
		pc.AttemptNumber = int(attempt.Int64)
	}
	if deferred.Valid {
		t := deferred.Time
		pc.DeferredUntil = &t
	}
}

func (db *DB) ResolvePendingConfirmation(id int64, status string) error {
	_, err := db.conn.Exec(
		`UPDATE pending_confirmations SET status = ? WHERE id = ?`, status, id)
	return err
}

func (db *DB) GetExpiredPendingConfirmations(userID int64, timeout time.Duration) ([]PendingConfirmation, error) {
	cutoff := time.Now().UTC().Add(-timeout)
	// Filtra kind='event' explicitamente: pendings de medicacao usam motor
	// de escalacao proprio (nao auto-confirm via timeout).
	rows, err := db.conn.Query(
		`SELECT pc.id, pc.user_id, pc.event_data, pc.status, pc.created_at,
		        pc.kind, pc.escalation_policy, pc.last_attempt_at, pc.attempt_number, pc.deferred_until,
		        u.phone_number, u.name
		 FROM pending_confirmations pc
		 JOIN users u ON u.id = pc.user_id
		 WHERE pc.status = 'pending' AND pc.user_id = ? AND pc.created_at <= ?
		   AND pc.kind = 'event'`, userID, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PendingConfirmation
	for rows.Next() {
		var pc PendingConfirmation
		var kind sql.NullString
		var policy sql.NullString
		var lastAttempt sql.NullTime
		var attempt sql.NullInt64
		var deferred sql.NullTime
		if err := rows.Scan(&pc.ID, &pc.UserID, &pc.EventData, &pc.Status, &pc.CreatedAt,
			&kind, &policy, &lastAttempt, &attempt, &deferred,
			&pc.PhoneNumber, &pc.UserName); err != nil {
			return nil, err
		}
		fillPendingExtras(&pc, kind, policy, lastAttempt, attempt, deferred)
		results = append(results, pc)
	}
	return results, rows.Err()
}

func (db *DB) HasSentReminder(userID int64, eventID string) (bool, error) {
	var count int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM sent_reminders WHERE user_id = ? AND event_id = ?`,
		userID, eventID).Scan(&count)
	return count > 0, err
}

func (db *DB) MarkReminderSent(userID int64, eventID string) error {
	_, err := db.conn.Exec(
		`INSERT OR IGNORE INTO sent_reminders (user_id, event_id) VALUES (?, ?)`,
		userID, eventID)
	return err
}

func (db *DB) GetUserByName(name string) (*User, error) {
	u := &User{}
	var ut sql.NullString
	var lastMsg sql.NullTime
	var thresh sql.NullInt64
	var paused sql.NullTime
	err := db.conn.QueryRow(
		`SELECT id, phone_number, name, google_calendar_id, google_credentials,
		 daily_summary_time, weekly_summary_day, weekly_summary_time,
		 reminder_before, auto_confirm_timeout, is_active, created_at,
		 type, last_user_message_at,
		 inactivity_threshold_hours, proactive_paused_until
		 FROM users WHERE name = ? AND is_active = 1 LIMIT 1`, name,
	).Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
		&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
		&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt,
		&ut, &lastMsg, &thresh, &paused)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err == nil {
		scanUserExtras(u, ut, lastMsg)
		scanUserPhase4(u, thresh, paused)
	}
	return u, err
}

func (db *DB) GetUserByID(id int64) (*User, error) {
	u := &User{}
	var ut sql.NullString
	var lastMsg sql.NullTime
	var thresh sql.NullInt64
	var paused sql.NullTime
	err := db.conn.QueryRow(
		`SELECT id, phone_number, name, google_calendar_id, google_credentials,
		 daily_summary_time, weekly_summary_day, weekly_summary_time,
		 reminder_before, auto_confirm_timeout, is_active, created_at,
		 type, last_user_message_at,
		 inactivity_threshold_hours, proactive_paused_until
		 FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
		&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
		&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt,
		&ut, &lastMsg, &thresh, &paused)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrUserNotFound
	}
	if err == nil {
		scanUserExtras(u, ut, lastMsg)
		scanUserPhase4(u, thresh, paused)
	}
	return u, err
}

// PermissionRequest represents a pending cross-user calendar access request.
type PermissionRequest struct {
	ID            int64
	RequesterID   int64
	TargetID      int64
	EventData     string
	Status        string
	CreatedAt     time.Time
	RequesterName  string
	RequesterPhone string
	TargetName    string
	TargetPhone   string
}

func (db *DB) CreatePermissionRequest(req *PermissionRequest) error {
	// Cancel any previous pending request from same requester to same target
	db.conn.Exec(
		`UPDATE pending_permission_requests SET status = 'cancelled'
		 WHERE requester_id = ? AND target_id = ? AND status = 'pending'`,
		req.RequesterID, req.TargetID)

	result, err := db.conn.Exec(
		`INSERT INTO pending_permission_requests (requester_id, target_id, event_data, status)
		 VALUES (?, ?, ?, 'pending')`,
		req.RequesterID, req.TargetID, req.EventData)
	if err != nil {
		return err
	}
	req.ID, _ = result.LastInsertId()
	return nil
}

func (db *DB) GetPendingPermissionRequest(targetID int64) (*PermissionRequest, error) {
	req := &PermissionRequest{}
	err := db.conn.QueryRow(
		`SELECT ppr.id, ppr.requester_id, ppr.target_id, ppr.event_data, ppr.status, ppr.created_at,
		 ru.name, ru.phone_number, tu.name, tu.phone_number
		 FROM pending_permission_requests ppr
		 JOIN users ru ON ru.id = ppr.requester_id
		 JOIN users tu ON tu.id = ppr.target_id
		 WHERE ppr.target_id = ? AND ppr.status = 'pending'
		 ORDER BY ppr.created_at DESC LIMIT 1`, targetID,
	).Scan(&req.ID, &req.RequesterID, &req.TargetID, &req.EventData, &req.Status, &req.CreatedAt,
		&req.RequesterName, &req.RequesterPhone, &req.TargetName, &req.TargetPhone)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoPendingConfirmation
	}
	return req, err
}

func (db *DB) ResolvePermissionRequest(id int64, status string) error {
	_, err := db.conn.Exec(
		`UPDATE pending_permission_requests SET status = ? WHERE id = ?`, status, id)
	return err
}

func (db *DB) GetExpiredPermissionRequests(timeout time.Duration) ([]PermissionRequest, error) {
	cutoff := time.Now().UTC().Add(-timeout)
	rows, err := db.conn.Query(
		`SELECT ppr.id, ppr.requester_id, ppr.target_id, ppr.event_data, ppr.status, ppr.created_at,
		 ru.name, ru.phone_number, tu.name, tu.phone_number
		 FROM pending_permission_requests ppr
		 JOIN users ru ON ru.id = ppr.requester_id
		 JOIN users tu ON tu.id = ppr.target_id
		 WHERE ppr.status = 'pending' AND ppr.created_at <= ?`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []PermissionRequest
	for rows.Next() {
		var req PermissionRequest
		if err := rows.Scan(&req.ID, &req.RequesterID, &req.TargetID, &req.EventData, &req.Status, &req.CreatedAt,
			&req.RequesterName, &req.RequesterPhone, &req.TargetName, &req.TargetPhone); err != nil {
			return nil, err
		}
		results = append(results, req)
	}
	return results, rows.Err()
}

func defaultStr(val, fallback string) string {
	if val == "" {
		return fallback
	}
	return val
}
