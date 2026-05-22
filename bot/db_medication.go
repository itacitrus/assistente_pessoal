package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// =========================================================================
// CRUD Medication
// =========================================================================

// CreateMedication insere o cadastro mestre. created_by_user_id default = user_id
// se nao setado. Devolve o ID populado em m.ID.
func (db *DB) CreateMedication(m *Medication) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("medication name required")
	}
	if m.UserID == 0 {
		return fmt.Errorf("medication user_id required")
	}
	createdBy := m.CreatedByUserID
	if createdBy == 0 {
		createdBy = m.UserID
	}
	tolerance := m.ToleranceMinutes
	if tolerance <= 0 {
		tolerance = DefaultToleranceMinutes
	}
	policy, err := ValidateLateDosePolicy(string(m.LateDosePolicy))
	if err != nil {
		return err
	}
	res, err := db.conn.Exec(
		`INSERT INTO medications (user_id, name, dose, instructions, active, created_by_user_id, tolerance_minutes, late_dose_policy, catalog_id, require_confirmation)
		 VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		m.UserID, m.Name, m.Dose, m.Instructions, createdBy, tolerance, string(policy), nullableID(m.CatalogID), boolToInt(m.RequireConfirmation))
	if err != nil {
		return fmt.Errorf("insert medication: %w", err)
	}
	m.ID, _ = res.LastInsertId()
	m.Active = true
	m.CreatedByUserID = createdBy
	m.ToleranceMinutes = tolerance
	m.LateDosePolicy = policy
	return nil
}

// rowQuerier abstrai QueryRowContext pra reusar a checagem de duplicidade
// tanto numa conexao fixada (transacao) quanto na conexao normal do pool.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// duplicateMedicationID procura um medicamento ATIVO do mesmo dono com
// nome+dose iguais (ignorando caixa e espacos nas bordas) E que tenha um
// schedule com a MESMA RRULE (mesmo horario) — a definicao de "copia
// identica". excludeID != 0 ignora aquele id (edicao do proprio remedio).
// Retorna o id encontrado, ou 0 se nao ha duplicata.
func duplicateMedicationID(ctx context.Context, q rowQuerier, userID, excludeID int64, name, dose, rrule string) (int64, error) {
	var id int64
	err := q.QueryRowContext(ctx, `
		SELECT m.id FROM medications m
		JOIN medication_schedules ms ON ms.medication_id = m.id
		WHERE m.user_id = ? AND m.active = 1 AND m.id != ?
		  AND LOWER(TRIM(m.name)) = LOWER(TRIM(?))
		  AND LOWER(TRIM(COALESCE(m.dose,''))) = LOWER(TRIM(?))
		  AND ms.rrule = ?
		LIMIT 1`,
		userID, excludeID, name, dose, rrule).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("check duplicate medication: %w", err)
	}
	return id, nil
}

// CreateMedicationWithSchedule cria o medicamento + 1 schedule de forma ATOMICA
// e bloqueia cadastro duplicado: se ja existe um medicamento ativo do mesmo
// dono com nome+dose iguais e o MESMO horario (RRULE), devolve
// ErrMedicationDuplicate sem inserir nada.
//
// A checagem + os inserts rodam numa transacao BEGIN IMMEDIATE sobre uma
// conexao FIXADA: o lock de escrita do SQLite eh adquirido ANTES do SELECT,
// entao dois cadastros concorrentes serializam e o segundo enxerga o primeiro.
// Isso fecha a janela de corrida entre checar e inserir — a chave de unicidade
// cruza duas tabelas (medications + medication_schedules), entao um indice
// UNIQUE simples nao cobriria.
func (db *DB) CreateMedicationWithSchedule(m *Medication, s *MedicationSchedule) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("medication name required")
	}
	if m.UserID == 0 {
		return fmt.Errorf("medication user_id required")
	}
	if strings.TrimSpace(s.RRULE) == "" {
		return fmt.Errorf("schedule rrule required")
	}
	createdBy := m.CreatedByUserID
	if createdBy == 0 {
		createdBy = m.UserID
	}
	tolerance := m.ToleranceMinutes
	if tolerance <= 0 {
		tolerance = DefaultToleranceMinutes
	}
	policy, err := ValidateLateDosePolicy(string(m.LateDosePolicy))
	if err != nil {
		return err
	}
	if s.StartDate.IsZero() {
		s.StartDate = time.Now().In(BRT())
	}
	startStr := s.StartDate.Format(dateLayout)
	var endStr sql.NullString
	if s.EndDate != nil {
		endStr = sql.NullString{String: s.EndDate.Format(dateLayout), Valid: true}
	}

	ctx := context.Background()
	conn, err := db.conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	// rollback desfaz tudo e propaga a causa — usado em todo caminho de erro.
	rollback := func(cause error) error {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return cause
	}

	dupID, err := duplicateMedicationID(ctx, conn, m.UserID, 0, m.Name, m.Dose, s.RRULE)
	if err != nil {
		return rollback(err)
	}
	if dupID != 0 {
		return rollback(ErrMedicationDuplicate)
	}

	res, err := conn.ExecContext(ctx,
		`INSERT INTO medications (user_id, name, dose, instructions, active, created_by_user_id, tolerance_minutes, late_dose_policy, catalog_id, require_confirmation)
		 VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?)`,
		m.UserID, m.Name, m.Dose, m.Instructions, createdBy, tolerance, string(policy), nullableID(m.CatalogID), boolToInt(m.RequireConfirmation))
	if err != nil {
		return rollback(fmt.Errorf("insert medication: %w", err))
	}
	medID, _ := res.LastInsertId()

	sres, err := conn.ExecContext(ctx,
		`INSERT INTO medication_schedules (medication_id, rrule, start_date, end_date, critical)
		 VALUES (?, ?, ?, ?, ?)`,
		medID, s.RRULE, startStr, endStr, boolToInt(s.Critical))
	if err != nil {
		return rollback(fmt.Errorf("insert medication_schedule: %w", err))
	}
	schedID, _ := sres.LastInsertId()

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return rollback(fmt.Errorf("commit: %w", err))
	}

	m.ID = medID
	m.Active = true
	m.CreatedByUserID = createdBy
	m.ToleranceMinutes = tolerance
	m.LateDosePolicy = policy
	s.ID = schedID
	s.MedicationID = medID
	return nil
}

// DuplicateActiveMedicationExists checa, fora de transacao, se ja existe um
// medicamento ativo do dono com nome+dose iguais e a MESMA RRULE, ignorando
// excludeID (o proprio remedio sendo editado). Usado no caminho de EDICAO; o
// cadastro novo usa CreateMedicationWithSchedule (transacional, race-proof).
func (db *DB) DuplicateActiveMedicationExists(userID, excludeID int64, name, dose, rrule string) (bool, error) {
	id, err := duplicateMedicationID(context.Background(), db.conn, userID, excludeID, name, dose, rrule)
	return id != 0, err
}

// scanMedicationRow centraliza o scan de uma row de medications na ordem
// canonica das colunas. Mantem GetMedicationByID e ListActiveMedications em
// sincronia quando colunas sao adicionadas.
func scanMedicationRow(s interface{ Scan(...any) error }, m *Medication) error {
	var active int
	var policy string
	var catalogID sql.NullInt64
	var requireConfirm int
	if err := s.Scan(&m.ID, &m.UserID, &m.Name, &m.Dose, &m.Instructions, &active,
		&m.CreatedByUserID, &m.ToleranceMinutes, &policy, &catalogID, &requireConfirm, &m.CreatedAt, &m.UpdatedAt); err != nil {
		return err
	}
	m.Active = active != 0
	m.LateDosePolicy = LateDosePolicy(policy)
	m.CatalogID = catalogID.Int64 // 0 quando NULL
	m.RequireConfirmation = requireConfirm != 0
	return nil
}

const medicationColumns = `id, user_id, name, dose, instructions, active, created_by_user_id, tolerance_minutes, late_dose_policy, catalog_id, require_confirmation, created_at, updated_at`

// nullableID converte um id 0 em NULL para colunas FK opcionais.
func nullableID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// GetMedicationByID busca uma medicacao por id. ErrMedicationNotFound se
// nao existe (independente de active).
func (db *DB) GetMedicationByID(id int64) (*Medication, error) {
	m := &Medication{}
	err := scanMedicationRow(db.conn.QueryRow(
		`SELECT `+medicationColumns+` FROM medications WHERE id = ?`, id), m)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMedicationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get medication: %w", err)
	}
	return m, nil
}

// ListActiveMedications devolve apenas medicacoes ativas (active=1) do user.
// Ordena por nome pra UX previsivel.
func (db *DB) ListActiveMedications(userID int64) ([]Medication, error) {
	rows, err := db.conn.Query(
		`SELECT `+medicationColumns+`
		 FROM medications WHERE user_id = ? AND active = 1
		 ORDER BY LOWER(name) ASC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list active medications: %w", err)
	}
	defer rows.Close()

	var meds []Medication
	for rows.Next() {
		var m Medication
		if err := scanMedicationRow(rows, &m); err != nil {
			return nil, err
		}
		meds = append(meds, m)
	}
	return meds, rows.Err()
}

// UpdateMedicationFields aplica edicoes parciais. Campo passado vazio (string)
// ou zero (numero/bool zerado) NAO sobrescreve — caller usa apenas os campos
// que quer mudar. Atualiza updated_at sempre. Retorna ErrMedicationNotFound
// se id nao existe.
func (db *DB) UpdateMedicationFields(id int64, newName, newDose, newInstructions *string, newTolerance *int, newPolicy *LateDosePolicy, newRequireConfirmation *bool) error {
	sets := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := []any{}
	if newName != nil {
		sets = append(sets, "name = ?")
		args = append(args, *newName)
	}
	if newDose != nil {
		sets = append(sets, "dose = ?")
		args = append(args, *newDose)
	}
	if newInstructions != nil {
		sets = append(sets, "instructions = ?")
		args = append(args, *newInstructions)
	}
	if newTolerance != nil {
		t := *newTolerance
		if t <= 0 {
			t = DefaultToleranceMinutes
		}
		sets = append(sets, "tolerance_minutes = ?")
		args = append(args, t)
	}
	if newPolicy != nil {
		p, err := ValidateLateDosePolicy(string(*newPolicy))
		if err != nil {
			return err
		}
		sets = append(sets, "late_dose_policy = ?")
		args = append(args, string(p))
	}
	if newRequireConfirmation != nil {
		sets = append(sets, "require_confirmation = ?")
		args = append(args, boolToInt(*newRequireConfirmation))
	}
	if len(sets) == 1 {
		// nada pra mudar — protecao contra UPDATE no-op acidental
		return nil
	}
	args = append(args, id)
	res, err := db.conn.Exec(
		`UPDATE medications SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("update medication: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrMedicationNotFound
	}
	return nil
}

// DeactivateMedication eh o soft-delete (active=0). Lembretes futuros param;
// historico permanece pra auditoria. Idempotente.
func (db *DB) DeactivateMedication(id int64) error {
	res, err := db.conn.Exec(
		`UPDATE medications SET active = 0, updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deactivate medication: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrMedicationNotFound
	}
	return nil
}

// CanManageMedicationFor retorna true se actorID pode gerenciar medicamentos
// de targetID. Caso self (actor==target), sempre true. Caso outro, exige
// vinculo em family_links (qualquer direcao serve — guardian->dependent).
func (db *DB) CanManageMedicationFor(actorID, targetID int64) (bool, error) {
	if actorID == targetID {
		return true, nil
	}
	// Checa nas duas direcoes: ator pode ser guardian do target, OU vice-versa
	// (caso raro, mas legitima — neto cuidando da avo, avo cuidando do neto
	// quando ela viaja). Fluxo principal: actor=guardian, target=dependent.
	yes, err := db.IsGuardianOf(actorID, targetID)
	if err != nil {
		return false, err
	}
	if yes {
		return true, nil
	}
	return db.IsGuardianOf(targetID, actorID)
}

// =========================================================================
// CRUD MedicationSchedule
// =========================================================================

// CreateMedicationSchedule insere um schedule para o medication. Valida que
// a RRULE ao menos parseia (ParseRRULE existe em rrule.go).
func (db *DB) CreateMedicationSchedule(s *MedicationSchedule) error {
	if s.MedicationID == 0 {
		return fmt.Errorf("schedule medication_id required")
	}
	if strings.TrimSpace(s.RRULE) == "" {
		return fmt.Errorf("schedule rrule required")
	}
	if s.StartDate.IsZero() {
		s.StartDate = time.Now().In(BRT())
	}
	startStr := s.StartDate.Format(dateLayout)
	var endStr sql.NullString
	if s.EndDate != nil {
		endStr = sql.NullString{String: s.EndDate.Format(dateLayout), Valid: true}
	}
	res, err := db.conn.Exec(
		`INSERT INTO medication_schedules (medication_id, rrule, start_date, end_date, critical)
		 VALUES (?, ?, ?, ?, ?)`,
		s.MedicationID, s.RRULE, startStr, endStr, boolToInt(s.Critical))
	if err != nil {
		return fmt.Errorf("insert medication_schedule: %w", err)
	}
	s.ID, _ = res.LastInsertId()
	return nil
}

// ListSchedulesForMedication devolve todos os schedules ativos do medication.
// Ordem por id asc (estavel — preserva ordem de cadastro).
func (db *DB) ListSchedulesForMedication(medID int64) ([]MedicationSchedule, error) {
	rows, err := db.conn.Query(
		`SELECT id, medication_id, rrule, start_date, end_date, critical, created_at
		 FROM medication_schedules WHERE medication_id = ?
		 ORDER BY id ASC`, medID)
	if err != nil {
		return nil, fmt.Errorf("list schedules: %w", err)
	}
	defer rows.Close()

	var out []MedicationSchedule
	for rows.Next() {
		var s MedicationSchedule
		var startStr string
		var endStr sql.NullString
		var critical int
		if err := rows.Scan(&s.ID, &s.MedicationID, &s.RRULE, &startStr, &endStr, &critical, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.StartDate, _ = time.ParseInLocation(dateLayout, startStr, BRT())
		if endStr.Valid && endStr.String != "" {
			t, _ := time.ParseInLocation(dateLayout, endStr.String, BRT())
			s.EndDate = &t
		}
		s.Critical = critical != 0
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSchedulesForMedication remove todos os schedules de um medication.
// Usado quando edicao substitui o RRULE inteiro (ver tools_medication.go).
func (db *DB) DeleteSchedulesForMedication(medID int64) error {
	_, err := db.conn.Exec(`DELETE FROM medication_schedules WHERE medication_id = ?`, medID)
	if err != nil {
		return fmt.Errorf("delete schedules: %w", err)
	}
	return nil
}

// RescheduleMedicationByDelta desloca os horarios de TODOS os schedules de um
// medication por delta, permanentemente (politica take_recalculate). Preserva
// frequencia, intervalo e dias. Retorna a descricao PT-BR do novo horario do
// primeiro schedule (para a mensagem ao titular), ou "" se nao houver schedule.
func (db *DB) RescheduleMedicationByDelta(medID int64, delta time.Duration) (string, error) {
	scheds, err := db.ListSchedulesForMedication(medID)
	if err != nil {
		return "", err
	}
	var firstDesc string
	for _, s := range scheds {
		shifted, err := shiftRRULEHours(s.RRULE, delta)
		if err != nil {
			return "", fmt.Errorf("shift rrule sched %d: %w", s.ID, err)
		}
		if _, err := db.conn.Exec(
			`UPDATE medication_schedules SET rrule = ? WHERE id = ?`, shifted, s.ID); err != nil {
			return "", fmt.Errorf("update schedule rrule: %w", err)
		}
		if firstDesc == "" {
			firstDesc = DescribeRRULE(shifted)
		}
	}
	return firstDesc, nil
}

// =========================================================================
// CRUD MedicationIntakeLog
// =========================================================================

// CreateIntakeLogIfAbsent eh o lock idempotente do scheduler.
// UNIQUE(medication_id, scheduled_at) garante que duas chamadas no mesmo
// segundo nao geram duplicidade — a segunda devolve ErrIntakeLogDuplicate.
//
// Chamadores devem tratar ErrIntakeLogDuplicate como sinal "ja disparou,
// pula" (nao eh erro). Outros erros sao reais (DB indisponivel etc).
func (db *DB) CreateIntakeLogIfAbsent(medID int64, scheduledAt time.Time, status IntakeStatus) error {
	_, err := db.conn.Exec(
		`INSERT INTO medication_intake_log (medication_id, scheduled_at, status)
		 VALUES (?, ?, ?)`,
		medID, scheduledAt.UTC(), string(status))
	if err == nil {
		return nil
	}
	if isUniqueViolation(err) {
		return ErrIntakeLogDuplicate
	}
	return fmt.Errorf("create intake log: %w", err)
}

// UpdateIntakeStatus atualiza o status de uma row de intake_log. Usado para
// transicoes pending -> taken/skipped/missed/escalated.
// Se nenhuma row casar (medID, scheduledAt), retorna sem erro — a row pode
// ter sido criada em ambiente diferente, e queremos ser idempotentes.
func (db *DB) UpdateIntakeStatus(medID int64, scheduledAt time.Time, status IntakeStatus, responseText string) error {
	_, err := db.conn.Exec(
		`UPDATE medication_intake_log
		 SET status = ?, confirmed_at = ?, response_text = ?
		 WHERE medication_id = ? AND scheduled_at = ?`,
		string(status), time.Now().UTC(), responseText, medID, scheduledAt.UTC())
	if err != nil {
		return fmt.Errorf("update intake status: %w", err)
	}
	return nil
}

// RecordTakenIntake registra (ou atualiza) uma dose como tomada para um slot
// agendado, de forma idempotente via UPSERT em UNIQUE(medication_id,
// scheduled_at). Usado quando o usuario declara "tomei" FORA de um lembrete
// ativo (ato proativo): sem isto, a tomada so iria pro audit_log e ficaria
// invisivel para a aderencia (GetMedicationStats7d le apenas o intake_log).
//
// Idempotente com o scheduler: se este slot ja existe como 'pending', vira
// 'taken'; se o scheduler disparar depois, o CreateIntakeLogIfAbsent dele
// bate no UNIQUE e nao duplica.
func (db *DB) RecordTakenIntake(medID int64, scheduledAt time.Time, note string) error {
	_, err := db.conn.Exec(
		`INSERT INTO medication_intake_log (medication_id, scheduled_at, status, confirmed_at, response_text)
		 VALUES (?, ?, 'taken', ?, ?)
		 ON CONFLICT(medication_id, scheduled_at)
		 DO UPDATE SET status = 'taken', confirmed_at = excluded.confirmed_at, response_text = excluded.response_text`,
		medID, scheduledAt.UTC(), time.Now().UTC(), note)
	if err != nil {
		return fmt.Errorf("record taken intake: %w", err)
	}
	return nil
}

// GetLatestPendingIntake busca a ocorrencia pending mais recente para o
// medicamento. Util para "marcar tomado" sem ID explicito (idoso responde
// "tomei" sem citar qual). Retorna ErrIntakeLogDuplicate se nao houver
// nenhuma pending — caller deve tratar como "nada para confirmar".
func (db *DB) GetLatestPendingIntake(medID int64) (*MedicationIntakeLog, error) {
	row := db.conn.QueryRow(
		`SELECT id, medication_id, scheduled_at, status, confirmed_at, response_text, created_at
		 FROM medication_intake_log
		 WHERE medication_id = ? AND status = 'pending'
		 ORDER BY scheduled_at DESC LIMIT 1`, medID)

	var il MedicationIntakeLog
	var status string
	var confirmed sql.NullTime
	var resp sql.NullString
	if err := row.Scan(&il.ID, &il.MedicationID, &il.ScheduledAt, &status, &confirmed, &resp, &il.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get latest pending intake: %w", err)
	}
	il.Status = IntakeStatus(status)
	if confirmed.Valid {
		t := confirmed.Time
		il.ConfirmedAt = &t
	}
	if resp.Valid {
		il.ResponseText = resp.String
	}
	return &il, nil
}

// ListIntakeLogsForMedication devolve historico ordenado por scheduled_at desc.
// Util para auditoria e painel do responsavel (Fase 5).
func (db *DB) ListIntakeLogsForMedication(medID int64, limit int) ([]MedicationIntakeLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.conn.Query(
		`SELECT id, medication_id, scheduled_at, status, confirmed_at, response_text, created_at
		 FROM medication_intake_log WHERE medication_id = ?
		 ORDER BY scheduled_at DESC LIMIT ?`, medID, limit)
	if err != nil {
		return nil, fmt.Errorf("list intake logs: %w", err)
	}
	defer rows.Close()

	var out []MedicationIntakeLog
	for rows.Next() {
		var il MedicationIntakeLog
		var status string
		var confirmed sql.NullTime
		var resp sql.NullString
		if err := rows.Scan(&il.ID, &il.MedicationID, &il.ScheduledAt, &status, &confirmed, &resp, &il.CreatedAt); err != nil {
			return nil, err
		}
		il.Status = IntakeStatus(status)
		if confirmed.Valid {
			t := confirmed.Time
			il.ConfirmedAt = &t
		}
		if resp.Valid {
			il.ResponseText = resp.String
		}
		out = append(out, il)
	}
	return out, rows.Err()
}

// ListIntakeHistory devolve as ocorrencias de dose (com nome+dose do remedio
// resolvidos via join) de userID a partir de `from`, ordenadas por
// scheduled_at desc. medID != 0 filtra um unico medicamento. Inclui doses de
// remedios ja desativados (historico imutavel). Limita a `limit` (default 500).
func (db *DB) ListIntakeHistory(userID, medID int64, from time.Time, limit int) ([]IntakeHistoryEntry, error) {
	if limit <= 0 {
		limit = 500
	}
	q := `
		SELECT m.id, m.name, m.dose, l.scheduled_at, l.status, l.confirmed_at
		FROM medication_intake_log l
		JOIN medications m ON m.id = l.medication_id
		WHERE m.user_id = ? AND l.scheduled_at >= ?`
	args := []any{userID, from.UTC()}
	if medID != 0 {
		q += ` AND m.id = ?`
		args = append(args, medID)
	}
	q += ` ORDER BY l.scheduled_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := db.conn.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("list intake history: %w", err)
	}
	defer rows.Close()

	var out []IntakeHistoryEntry
	for rows.Next() {
		var e IntakeHistoryEntry
		var status string
		var confirmed sql.NullTime
		if err := rows.Scan(&e.MedicationID, &e.MedicationName, &e.Dose, &e.ScheduledAt, &status, &confirmed); err != nil {
			return nil, err
		}
		e.Status = IntakeStatus(status)
		if confirmed.Valid {
			t := confirmed.Time
			e.ConfirmedAt = &t
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkStaleNoConfirmDosesUnknown varre doses 'pending' de medicamentos que NAO
// exigem confirmacao (require_confirmation=0) e, passada a janela de tolerancia
// (scheduled_at + tolerance_minutes), marca-as 'unknown' — nem tomada nem
// perdida. Esses remedios nao geram pending_confirmation (sem cutucao/familia),
// entao o motor de escalacao nunca os toca; este sweeper eh quem fecha o ciclo.
//
// A aritmetica de prazo eh feita em Go (nao em SQL) pra evitar divergencia de
// formato no armazenamento de DATETIME do driver. Idempotente: so mexe em
// linhas ainda 'pending'. Retorna quantas marcou.
func (db *DB) MarkStaleNoConfirmDosesUnknown(now time.Time) (int, error) {
	rows, err := db.conn.Query(`
		SELECT l.id, l.scheduled_at, m.tolerance_minutes
		FROM medication_intake_log l
		JOIN medications m ON m.id = l.medication_id
		WHERE l.status = 'pending' AND m.require_confirmation = 0`)
	if err != nil {
		return 0, fmt.Errorf("scan stale no-confirm doses: %w", err)
	}
	defer rows.Close()

	var stale []int64
	for rows.Next() {
		var id int64
		var scheduledAt time.Time
		var tol int
		if err := rows.Scan(&id, &scheduledAt, &tol); err != nil {
			return 0, err
		}
		if tol <= 0 {
			tol = DefaultToleranceMinutes
		}
		deadline := scheduledAt.Add(time.Duration(tol) * time.Minute)
		if !now.Before(deadline) {
			stale = append(stale, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, id := range stale {
		res, err := db.conn.Exec(
			`UPDATE medication_intake_log SET status = 'unknown'
			 WHERE id = ? AND status = 'pending'`, id)
		if err != nil {
			return n, fmt.Errorf("mark dose unknown: %w", err)
		}
		if c, _ := res.RowsAffected(); c > 0 {
			n++
		}
	}
	return n, nil
}

// =========================================================================
// pending_confirmations Fase 3 helpers
// =========================================================================

// GetActiveMedicationPendings devolve todas as pendings em status='pending'
// com kind='medication'. Usado pelo job checkMedicationEscalation pra varrer
// candidatos a nova tentativa.
func (db *DB) GetActiveMedicationPendings() ([]PendingConfirmation, error) {
	rows, err := db.conn.Query(
		`SELECT pc.id, pc.user_id, pc.event_data, pc.status, pc.created_at,
		        pc.kind, pc.escalation_policy, pc.last_attempt_at, pc.attempt_number, pc.deferred_until,
		        u.phone_number, u.name
		 FROM pending_confirmations pc
		 JOIN users u ON u.id = pc.user_id
		 WHERE pc.status = 'pending' AND pc.kind = 'medication'`)
	if err != nil {
		return nil, fmt.Errorf("get active medication pendings: %w", err)
	}
	defer rows.Close()

	var out []PendingConfirmation
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
		out = append(out, pc)
	}
	return out, rows.Err()
}

// GetActivePendingForMedication busca a pending kind='medication' atual
// para o user e medication_id. Util para o tool marcar_remedio_tomado quando
// o ID nao foi passado — pegamos o pending mais recente e usamos o
// medication_id de la.
func (db *DB) GetActivePendingForUserAndMedication(userID, medID int64) (*PendingConfirmation, error) {
	rows, err := db.conn.Query(
		`SELECT pc.id, pc.user_id, pc.event_data, pc.status, pc.created_at,
		        pc.kind, pc.escalation_policy, pc.last_attempt_at, pc.attempt_number, pc.deferred_until
		 FROM pending_confirmations pc
		 WHERE pc.user_id = ? AND pc.status = 'pending' AND pc.kind = 'medication'
		 ORDER BY pc.created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("get active medication pending: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var pc PendingConfirmation
		var kind sql.NullString
		var policy sql.NullString
		var lastAttempt sql.NullTime
		var attempt sql.NullInt64
		var deferred sql.NullTime
		if err := rows.Scan(&pc.ID, &pc.UserID, &pc.EventData, &pc.Status, &pc.CreatedAt,
			&kind, &policy, &lastAttempt, &attempt, &deferred); err != nil {
			return nil, err
		}
		fillPendingExtras(&pc, kind, policy, lastAttempt, attempt, deferred)
		// Filtra por medID olhando dentro do payload. Caller pediu "qual o
		// pending pra esse medID" — varremos os mais recentes ate achar.
		if medID == 0 || medMedicationID(&pc) == medID {
			return &pc, nil
		}
	}
	return nil, nil
}

// HasActiveMedicationPending informa se o usuario tem alguma confirmacao de
// dose em status='pending'. Usado por medContextActive pra decidir se o turno
// do idoso toca em remedio (e, com isso, se as regras farmacologicas entram no
// prompt). Uma dose pendente significa que a resposta dele provavelmente eh
// sobre o remedio, mesmo que indireta ("agitado", "daqui a pouco").
func (db *DB) HasActiveMedicationPending(userID int64) (bool, error) {
	var n int
	err := db.conn.QueryRow(
		`SELECT COUNT(1) FROM pending_confirmations
		 WHERE user_id = ? AND status = 'pending' AND kind = 'medication'`, userID).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("has active medication pending: %w", err)
	}
	return n > 0, nil
}

// ListActiveMedicationPendingsForUser devolve TODAS as pendings de medicacao
// em aberto do usuario (mais recentes primeiro). Usado quando o idoso confirma
// genericamente ("tomei") sem citar qual remedio — marcamos todas as doses
// pendentes daquele momento como tomadas.
func (db *DB) ListActiveMedicationPendingsForUser(userID int64) ([]PendingConfirmation, error) {
	rows, err := db.conn.Query(
		`SELECT pc.id, pc.user_id, pc.event_data, pc.status, pc.created_at,
		        pc.kind, pc.escalation_policy, pc.last_attempt_at, pc.attempt_number, pc.deferred_until
		 FROM pending_confirmations pc
		 WHERE pc.user_id = ? AND pc.status = 'pending' AND pc.kind = 'medication'
		 ORDER BY pc.created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list active medication pendings: %w", err)
	}
	defer rows.Close()

	var out []PendingConfirmation
	for rows.Next() {
		var pc PendingConfirmation
		var kind, policy sql.NullString
		var lastAttempt, deferred sql.NullTime
		var attempt sql.NullInt64
		if err := rows.Scan(&pc.ID, &pc.UserID, &pc.EventData, &pc.Status, &pc.CreatedAt,
			&kind, &policy, &lastAttempt, &attempt, &deferred); err != nil {
			return nil, err
		}
		fillPendingExtras(&pc, kind, policy, lastAttempt, attempt, deferred)
		out = append(out, pc)
	}
	return out, rows.Err()
}

// SetPendingDeferredUntil grava o horario que o idoso disse que vai tomar.
// Usado pela tool adiar_remedio. Passar zero-time limpa o adiamento (NULL).
func (db *DB) SetPendingDeferredUntil(pcID int64, until time.Time) error {
	var arg any
	if until.IsZero() {
		arg = nil
	} else {
		arg = until.UTC()
	}
	_, err := db.conn.Exec(
		`UPDATE pending_confirmations SET deferred_until = ? WHERE id = ?`, arg, pcID)
	if err != nil {
		return fmt.Errorf("set pending deferred_until: %w", err)
	}
	return nil
}

// UpdatePendingAttempt atualiza attempt_number e last_attempt_at. Usado pelo
// EscalationEngine apos uma tentativa bem-sucedida.
func (db *DB) UpdatePendingAttempt(pcID int64, attempt int, now time.Time) error {
	_, err := db.conn.Exec(
		`UPDATE pending_confirmations
		 SET attempt_number = ?, last_attempt_at = ?
		 WHERE id = ?`,
		attempt, now.UTC(), pcID)
	if err != nil {
		return fmt.Errorf("update pending attempt: %w", err)
	}
	return nil
}

// =========================================================================
// CRUD Escalation
// =========================================================================

// RecordEscalationAttempt insere uma row em escalations. UNIQUE(pc, attempt,
// recipient) garante que duas chamadas concorrentes pra mesma combinacao
// nao geram duplicata — a segunda devolve erro de UNIQUE, que o caller
// trata como "ja registrado".
func (db *DB) RecordEscalationAttempt(pcID int64, policyName string, attempt int, recipientID int64, channel string, now time.Time) error {
	_, err := db.conn.Exec(
		`INSERT INTO escalations
		 (pending_confirmation_id, policy_name, attempt_number, scheduled_for,
		  status, notifier_used, recipient_user_id, sent_at)
		 VALUES (?, ?, ?, ?, 'sent', ?, ?, ?)`,
		pcID, policyName, attempt, now.UTC(), channel, recipientID, now.UTC())
	if err != nil {
		if isUniqueViolation(err) {
			// Sinal idempotente. Caller deve tratar como "ja registrei, ok".
			return ErrIntakeLogDuplicate
		}
		return fmt.Errorf("record escalation: %w", err)
	}
	return nil
}

// ListEscalationsForPending devolve historico ordenado por attempt asc.
// Util para testes e painel.
func (db *DB) ListEscalationsForPending(pcID int64) ([]Escalation, error) {
	rows, err := db.conn.Query(
		`SELECT id, pending_confirmation_id, policy_name, attempt_number, scheduled_for,
		        status, notifier_used, recipient_user_id, sent_at, created_at
		 FROM escalations WHERE pending_confirmation_id = ?
		 ORDER BY attempt_number ASC, recipient_user_id ASC`, pcID)
	if err != nil {
		return nil, fmt.Errorf("list escalations: %w", err)
	}
	defer rows.Close()

	var out []Escalation
	for rows.Next() {
		var e Escalation
		var status string
		var sentAt sql.NullTime
		if err := rows.Scan(&e.ID, &e.PendingConfirmationID, &e.PolicyName, &e.AttemptNumber, &e.ScheduledFor,
			&status, &e.NotifierUsed, &e.RecipientUserID, &sentAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Status = EscalationStatus(status)
		if sentAt.Valid {
			t := sentAt.Time
			e.SentAt = &t
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListGuardiansForUser devolve guardians de userID que tenham flag=true.
// Hoje so flag="notify_on_medication_miss" eh suportado; outras flags
// (notify_on_inactivity, notify_on_severe_signal) viram nas Fases 4/5.
//
// flag eh um nome de coluna em family_links — validamos contra um set fixo
// pra evitar SQL injection (nao da pra usar bind no nome da coluna).
func (db *DB) ListGuardiansForUser(userID int64, flag string) ([]User, error) {
	allowed := map[string]bool{
		"notify_on_medication_miss": true,
		"notify_on_inactivity":      true,
		"notify_on_severe_signal":   true,
	}
	if !allowed[flag] {
		return nil, fmt.Errorf("invalid flag %q for ListGuardiansForUser", flag)
	}

	q := fmt.Sprintf(
		`SELECT u.id, u.phone_number, u.name, u.google_calendar_id, u.google_credentials,
		        u.daily_summary_time, u.weekly_summary_day, u.weekly_summary_time,
		        u.reminder_before, u.auto_confirm_timeout, u.is_active, u.created_at,
		        u.type, u.last_user_message_at
		 FROM users u
		 JOIN family_links fl ON fl.guardian_id = u.id
		 WHERE fl.dependent_id = ? AND fl.%s = 1 AND u.is_active = 1
		 ORDER BY LOWER(u.name) ASC`, flag)

	rows, err := db.conn.Query(q, userID)
	if err != nil {
		return nil, fmt.Errorf("list guardians: %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		var ut sql.NullString
		var lastMsg sql.NullTime
		if err := rows.Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
			&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
			&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt,
			&ut, &lastMsg); err != nil {
			return nil, err
		}
		scanUserExtras(&u, ut, lastMsg)
		out = append(out, u)
	}
	return out, rows.Err()
}

// =========================================================================
// Helpers
// =========================================================================

// isUniqueViolation detecta UNIQUE constraint failed. modernc.org/sqlite nao
// expoe codigo numerico estavel, entao matchamos por substring na mensagem.
// Mesma estrategia ja usada em family.go.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed") ||
		strings.Contains(err.Error(), "UNIQUE")
}

// boolToInt converte bool em 0/1 pra colunas INTEGER que representam flags.
// SQLite aceita true/false em alguns drivers, mas integer eh portable.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
