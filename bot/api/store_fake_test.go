package api

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// fakeStore eh in-memory mock pra testar handlers sem subir SQLite. Mantemos
// o estado estritamente o necessario pra simular o fluxo do plano.
type fakeStore struct {
	mu sync.Mutex

	users      map[int64]*User  // id -> user
	usersByPh  map[string]int64 // phone -> id
	nextUserID int64

	sessions   map[int64]*fakeSession // id -> session
	sessByHash map[string]int64       // hash (== plaintext em fake) -> id
	nextSessID int64

	loginAttempts []fakeAttempt

	links      map[int64]*FamilyLink
	linksByGD  map[string]int64 // "guardian-dependent" -> id
	nextLinkID int64

	consents map[string]string // "guardian-dependent" -> active|revoked

	snapshots map[int64][]SnapshotPoint // userID -> sorted points

	// Audit log buffer.
	audits []fakeAudit

	// Magic link sink + counters.
	magicLinks []magicLink
	// WhatsApp sink (boas-vindas e outras mensagens transacionais).
	whatsappSent []magicLink

	// Medicacao do dependente: fixtures por dependentID + counter.
	dependentMeds map[int64][]MedicationItem
	createMedErr  error

	// ProfileFacts fixture por userID.
	profileFacts map[int64]ProfileFactsResponse

	// Synthesize counter — proves cache works.
	synthesizeCalls atomic.Int64

	// Me / agenda + insights fixtures.
	upcoming     map[int64][]AgendaEvent
	activity     map[int64][]ActivityItem
	insightsData map[int64]synthesis.AgendaInsightsInput

	// Optional overrides for failure modes.
	sendMagicLinkErr error
	upcomingErr      error
	activityErr      error
	insightsDataErr  error
}

type fakeSession struct {
	ID        int64
	UserID    int64
	Hash      string
	Status    string
	ExpiresAt time.Time
}

type fakeAttempt struct {
	Phone     string
	IP        string
	CreatedAt time.Time
}

type fakeAudit struct {
	UserID  int64
	Action  string
	Target  string
	Details string
}

type magicLink struct {
	Phone   string
	Message string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		users:        map[int64]*User{},
		usersByPh:    map[string]int64{},
		sessions:     map[int64]*fakeSession{},
		sessByHash:   map[string]int64{},
		links:        map[int64]*FamilyLink{},
		linksByGD:    map[string]int64{},
		consents:     map[string]string{},
		snapshots:    map[int64][]SnapshotPoint{},
		upcoming:      map[int64][]AgendaEvent{},
		activity:      map[int64][]ActivityItem{},
		insightsData:  map[int64]synthesis.AgendaInsightsInput{},
		dependentMeds: map[int64][]MedicationItem{},
		profileFacts:  map[int64]ProfileFactsResponse{},
	}
}

// fakeReport eh um synthesis.ReportClient fake com contador de chamadas —
// prova que o cache de insights evita re-geracao. Retorna um JSON valido por
// default; out/err configuraveis pra simular falha/validacao.
type fakeReport struct {
	calls atomic.Int64
	resp  llm.ReportResponse
	err   error
}

func (f *fakeReport) Synthesize(_ context.Context, _ llm.ReportRequest) (llm.ReportResponse, error) {
	f.calls.Add(1)
	if f.err != nil {
		return llm.ReportResponse{}, f.err
	}
	if f.resp.Text == "" {
		return llm.ReportResponse{Text: `{"summary":"Voce concentra compromissos nas tardes.","insights":[{"title":"Tardes movimentadas","detail":"A maioria dos seus compromissos cai entre 14h e 18h.","kind":"pattern"}]}`}, nil
	}
	return f.resp, nil
}

func (f *fakeReport) Name() string { return "fake-report" }

// addUser eh helper de bootstrap pra testes.
func (s *fakeStore) addUser(name, phone string) *User {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextUserID++
	u := &User{
		ID:                       s.nextUserID,
		Name:                     name,
		PhoneNumber:              phone,
		Type:                     "comum",
		DailySummaryTime:         "07:00",
		WeeklySummaryDay:         "sunday",
		WeeklySummaryTime:        "20:00",
		ReminderBefore:           "1h",
		AutoConfirmTimeout:       "2h",
		InactivityThresholdHours: 24,
		IsActive:                 true,
		CreatedAt:                time.Now().UTC(),
	}
	s.users[u.ID] = u
	s.usersByPh[phone] = u.ID
	return u
}

func (s *fakeStore) addLink(guardianID, dependentID int64, relationship string) *FamilyLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextLinkID++
	fl := &FamilyLink{
		ID:           s.nextLinkID,
		GuardianID:   guardianID,
		DependentID:  dependentID,
		Relationship: relationship,
		Notify: Notify{
			OnMedicationMiss: true,
			OnInactivity:     true,
			OnSevereSignal:   true,
		},
		ConsentStatus: "active",
		CreatedAt:     time.Now().UTC(),
	}
	s.links[fl.ID] = fl
	s.linksByGD[joinGDKey(guardianID, dependentID)] = fl.ID
	s.consents[joinGDKey(guardianID, dependentID)] = "active"
	return fl
}

// =========== Store impl ===========

func (s *fakeStore) GetUserByPhone(_ context.Context, phone string) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.usersByPh[phone]
	if !ok {
		return nil, ErrNotFound
	}
	u := *s.users[id]
	return &u, nil
}

func (s *fakeStore) GetUserByID(_ context.Context, id int64) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *u
	return &cp, nil
}

func (s *fakeStore) CreatePendingSession(_ context.Context, userID int64, ip, ua string) (int64, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextSessID++
	plaintext := genFakeToken(s.nextSessID)
	hash := plaintext // fake — nao precisamos sha256 real
	sess := &fakeSession{
		ID:        s.nextSessID,
		UserID:    userID,
		Hash:      hash,
		Status:    "pending",
		ExpiresAt: time.Now().UTC().Add(15 * time.Minute),
	}
	s.sessions[sess.ID] = sess
	s.sessByHash[hash] = sess.ID
	return sess.ID, plaintext, nil
}

func (s *fakeStore) ActivateSession(_ context.Context, plaintext string) (int64, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.sessByHash[plaintext]
	if !ok {
		return 0, 0, ErrNotFound
	}
	sess := s.sessions[id]
	if sess.Status != "pending" {
		return 0, 0, ErrSessionInvalid
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		return 0, 0, ErrSessionExpired
	}
	sess.Status = "active"
	sess.ExpiresAt = time.Now().UTC().Add(30 * 24 * time.Hour)
	return sess.UserID, sess.ID, nil
}

func (s *fakeStore) GetActiveSessionByToken(_ context.Context, plaintext string) (int64, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.sessByHash[plaintext]
	if !ok {
		return 0, 0, ErrNotFound
	}
	sess := s.sessions[id]
	if sess.Status != "active" {
		return 0, 0, ErrSessionInvalid
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		return 0, 0, ErrSessionExpired
	}
	return sess.ID, sess.UserID, nil
}

func (s *fakeStore) TouchSession(_ context.Context, sessID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessID]
	if !ok {
		return ErrNotFound
	}
	sess.ExpiresAt = time.Now().UTC().Add(30 * 24 * time.Hour)
	return nil
}

func (s *fakeStore) RevokeSession(_ context.Context, sessID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[sessID]; ok {
		sess.Status = "revoked"
	}
	return nil
}

func (s *fakeStore) CountRecentLoginAttempts(_ context.Context, phone string, window time.Duration) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().UTC().Add(-window)
	n := 0
	for _, a := range s.loginAttempts {
		if a.Phone == phone && a.CreatedAt.After(cutoff) {
			n++
		}
	}
	return n, nil
}

func (s *fakeStore) CountRecentLoginAttemptsByIP(_ context.Context, ip string, window time.Duration) (int, error) {
	if ip == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().UTC().Add(-window)
	n := 0
	for _, a := range s.loginAttempts {
		if a.IP == ip && a.CreatedAt.After(cutoff) {
			n++
		}
	}
	return n, nil
}

func (s *fakeStore) RecordLoginAttempt(_ context.Context, phone, ip string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loginAttempts = append(s.loginAttempts, fakeAttempt{
		Phone: phone, IP: ip, CreatedAt: time.Now().UTC(),
	})
	return nil
}

func (s *fakeStore) UpdateUserPreferences(_ context.Context, userID int64, p PreferencesPatch) (*User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.users[userID]
	if !ok {
		return nil, ErrNotFound
	}
	if p.Name != nil {
		u.Name = *p.Name
	}
	if p.DailySummaryTime != nil {
		u.DailySummaryTime = *p.DailySummaryTime
	}
	if p.WeeklySummaryDay != nil {
		u.WeeklySummaryDay = *p.WeeklySummaryDay
	}
	if p.WeeklySummaryTime != nil {
		u.WeeklySummaryTime = *p.WeeklySummaryTime
	}
	if p.ReminderBefore != nil {
		u.ReminderBefore = *p.ReminderBefore
	}
	if p.AutoConfirmTimeout != nil {
		u.AutoConfirmTimeout = *p.AutoConfirmTimeout
	}
	if p.InactivityThresholdHours != nil {
		u.InactivityThresholdHours = *p.InactivityThresholdHours
	}
	cp := *u
	return &cp, nil
}

func (s *fakeStore) CreateDependent(_ context.Context, guardianID int64, req CreateDependentRequest) (*User, *FamilyLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.usersByPh[req.Phone]; exists {
		return nil, nil, ErrConflict
	}
	s.nextUserID++
	u := &User{
		ID:          s.nextUserID,
		Name:        req.Name,
		PhoneNumber: req.Phone,
		Type:        "idoso",
		IsActive:    true,
		CreatedAt:   time.Now().UTC(),
	}
	s.users[u.ID] = u
	s.usersByPh[req.Phone] = u.ID
	s.nextLinkID++
	fl := &FamilyLink{
		ID:           s.nextLinkID,
		GuardianID:   guardianID,
		DependentID:  u.ID,
		Relationship: req.Relationship,
		Notify: Notify{
			OnMedicationMiss: true,
			OnInactivity:     true,
			OnSevereSignal:   true,
		},
		ConsentStatus: "active",
		CreatedAt:     time.Now().UTC(),
	}
	s.links[fl.ID] = fl
	s.linksByGD[joinGDKey(guardianID, u.ID)] = fl.ID
	s.consents[joinGDKey(guardianID, u.ID)] = "active"
	uc := *u
	flc := *fl
	return &uc, &flc, nil
}

func (s *fakeStore) ListDependents(_ context.Context, guardianID int64) ([]DependentSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []DependentSummary
	for _, fl := range s.links {
		if fl.GuardianID != guardianID {
			continue
		}
		dep, ok := s.users[fl.DependentID]
		if !ok {
			continue
		}
		out = append(out, DependentSummary{User: *dep, Link: *fl})
	}
	return out, nil
}

func (s *fakeStore) UpdateDependent(ctx context.Context, guardianID, dependentID int64, p DependentPatch) (*User, error) {
	s.mu.Lock()
	_, ok := s.linksByGD[joinGDKey(guardianID, dependentID)]
	s.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	patch := PreferencesPatch{
		Name:                     p.Name,
		DailySummaryTime:         p.DailySummaryTime,
		WeeklySummaryDay:         p.WeeklySummaryDay,
		WeeklySummaryTime:        p.WeeklySummaryTime,
		ReminderBefore:           p.ReminderBefore,
		InactivityThresholdHours: p.InactivityThresholdHours,
	}
	return s.UpdateUserPreferences(ctx, dependentID, patch)
}

func (s *fakeStore) UpdateNotifyPrefs(_ context.Context, guardianID, linkID int64, p NotifyPatch) (*FamilyLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fl, ok := s.links[linkID]
	if !ok {
		return nil, ErrNotFound
	}
	if fl.GuardianID != guardianID {
		return nil, ErrNotFound
	}
	if p.OnMedicationMiss != nil {
		fl.Notify.OnMedicationMiss = *p.OnMedicationMiss
	}
	if p.OnInactivity != nil {
		fl.Notify.OnInactivity = *p.OnInactivity
	}
	if p.OnSevereSignal != nil {
		fl.Notify.OnSevereSignal = *p.OnSevereSignal
	}
	cp := *fl
	return &cp, nil
}

func (s *fakeStore) GetFamilyLink(_ context.Context, linkID int64) (*FamilyLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fl, ok := s.links[linkID]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *fl
	return &cp, nil
}

func (s *fakeStore) IsGuardianOf(_ context.Context, guardianID, dependentID int64) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.linksByGD[joinGDKey(guardianID, dependentID)]
	return ok, nil
}

func (s *fakeStore) GetDependentConsent(_ context.Context, guardianID, dependentID int64) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.consents[joinGDKey(guardianID, dependentID)]
	if !ok {
		return "active", nil
	}
	return v, nil
}

func (s *fakeStore) BuildDependentStatus(_ context.Context, guardianID, dependentID int64, days int) (*StatusResponse, error) {
	s.synthesizeCalls.Add(1)
	s.mu.Lock()
	dep, ok := s.users[dependentID]
	s.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	return &StatusResponse{
		Dependent: DependentRef{ID: dep.ID, Name: dep.Name},
		Days:      days,
		Synthesis: SynthesisSummary{
			Tendencia:        "estavel",
			Resumo:           "Tudo bem",
			NivelPreocupacao: "baixo",
		},
	}, nil
}

func (s *fakeStore) GetTimeline(_ context.Context, dependentID int64, days int) ([]SnapshotPoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pts := s.snapshots[dependentID]
	if pts == nil {
		return []SnapshotPoint{}, nil
	}
	out := make([]SnapshotPoint, len(pts))
	copy(out, pts)
	return out, nil
}

func (s *fakeStore) UpcomingEvents(_ context.Context, userID int64) ([]AgendaEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upcomingErr != nil {
		return nil, s.upcomingErr
	}
	out := make([]AgendaEvent, len(s.upcoming[userID]))
	copy(out, s.upcoming[userID])
	return out, nil
}

func (s *fakeStore) RecentActivity(ctx context.Context, userID int64, limit int) ([]ActivityItem, error) {
	if limit <= 0 {
		limit = 8
	}
	return s.ActivityHistory(ctx, userID, limit)
}

func (s *fakeStore) ActivityHistory(_ context.Context, userID int64, limit int) ([]ActivityItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activityErr != nil {
		return nil, s.activityErr
	}
	if limit <= 0 {
		limit = 50
	}
	out := make([]ActivityItem, 0, limit)
	for _, it := range s.activity[userID] {
		if !IsRelevantActivity(it.Action) {
			continue
		}
		if len(out) >= limit {
			break
		}
		out = append(out, it)
	}
	return out, nil
}

func (s *fakeStore) AgendaInsightsData(_ context.Context, userID int64, days int) (synthesis.AgendaInsightsInput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.insightsDataErr != nil {
		return synthesis.AgendaInsightsInput{}, s.insightsDataErr
	}
	in := s.insightsData[userID]
	in.PeriodDays = days
	return in, nil
}

func (s *fakeStore) Audit(_ context.Context, userID int64, action, target, details string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audits = append(s.audits, fakeAudit{userID, action, target, details})
}

func (s *fakeStore) SendMagicLink(_ context.Context, phone, msg string) error {
	if s.sendMagicLinkErr != nil {
		return s.sendMagicLinkErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.magicLinks = append(s.magicLinks, magicLink{Phone: phone, Message: msg})
	return nil
}

func (s *fakeStore) SendWhatsApp(_ context.Context, phone, msg string) error {
	if s.sendMagicLinkErr != nil {
		return s.sendMagicLinkErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.whatsappSent = append(s.whatsappSent, magicLink{Phone: phone, Message: msg})
	return nil
}

func (s *fakeStore) ProfileFacts(_ context.Context, userID int64) (ProfileFactsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	resp := s.profileFacts[userID]
	if resp.Relations == nil {
		resp.Relations = []RelationFact{}
	}
	if resp.People == nil {
		resp.People = []PersonFact{}
	}
	if resp.Trips == nil {
		resp.Trips = []TripFact{}
	}
	resp.Available = len(resp.Relations) > 0 || len(resp.People) > 0 || len(resp.Trips) > 0
	return resp, nil
}

func (s *fakeStore) ListDependentMedications(_ context.Context, guardianID, dependentID int64) ([]MedicationItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.linksByGD[joinGDKey(guardianID, dependentID)]; !ok {
		return nil, ErrNotFound
	}
	out := make([]MedicationItem, len(s.dependentMeds[dependentID]))
	copy(out, s.dependentMeds[dependentID])
	return out, nil
}

func (s *fakeStore) CreateDependentMedication(_ context.Context, guardianID, dependentID int64, in CreateMedicationRequest) (*MedicationItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createMedErr != nil {
		return nil, s.createMedErr
	}
	if _, ok := s.linksByGD[joinGDKey(guardianID, dependentID)]; !ok {
		return nil, ErrNotFound
	}
	item := MedicationItem{
		ID:           int64(len(s.dependentMeds[dependentID]) + 1),
		Name:         in.Name,
		Dose:         in.Dose,
		Instructions: in.Instructions,
		Schedule:     "Todos os dias",
		Active:       true,
	}
	s.dependentMeds[dependentID] = append(s.dependentMeds[dependentID], item)
	return &item, nil
}

func (s *fakeStore) DeactivateDependentMedication(_ context.Context, guardianID, dependentID, medID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.linksByGD[joinGDKey(guardianID, dependentID)]; !ok {
		return ErrNotFound
	}
	meds := s.dependentMeds[dependentID]
	for i := range meds {
		if meds[i].ID == medID {
			s.dependentMeds[dependentID] = append(meds[:i], meds[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// helpers ---

// joinGDKey eh chave para mapas (guardian, dependent).
func joinGDKey(g, d int64) string {
	return strKey(g, d)
}

func strKey(a, b int64) string {
	return fmt.Sprintf("%d-%d", a, b)
}

func genFakeToken(seed int64) string {
	// Token previsivel facilita debug; eh fake — sem implicacao de seguranca.
	return fmt.Sprintf("tok-%d-XYZ", seed)
}

// Ensure fakeStore satisfies Store at compile time.
var _ Store = (*fakeStore)(nil)
