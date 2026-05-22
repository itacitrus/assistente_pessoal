package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/api"
)

func TestBuildMedicationRRULE_Daily(t *testing.T) {
	got, err := buildMedicationRRULE([]string{"08:00", "20:00"}, "daily", nil)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	want := "FREQ=DAILY;BYHOUR=8,20;BYMINUTE=0"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, perr := ParseRRULE(got); perr != nil {
		t.Fatalf("rrule should parse: %v", perr)
	}
}

func TestBuildMedicationRRULE_Weekly(t *testing.T) {
	got, err := buildMedicationRRULE([]string{"08:00", "20:00"}, "weekly", []string{"wed", "mon"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	// Ordem canonica MO antes de WE.
	want := "FREQ=WEEKLY;BYDAY=MO,WE;BYHOUR=8,20;BYMINUTE=0"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, perr := ParseRRULE(got); perr != nil {
		t.Fatalf("rrule should parse: %v", perr)
	}
}

func TestBuildMedicationRRULE_Errors(t *testing.T) {
	if _, err := buildMedicationRRULE(nil, "daily", nil); err == nil {
		t.Fatalf("expected error for no times")
	}
	if _, err := buildMedicationRRULE([]string{"08:00"}, "weekly", nil); err == nil {
		t.Fatalf("expected error for weekly without days")
	}
	if _, err := buildMedicationRRULE([]string{"08:00"}, "monthly", nil); err == nil {
		t.Fatalf("expected error for invalid frequency")
	}
}

func TestCreateAndListDependentMedication(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Fabio", "Joaquim")
	guardian, dep := users[0], users[1]
	if _, err := db.LinkFamily(guardian.ID, dep.ID, "Pai"); err != nil {
		t.Fatalf("link: %v", err)
	}

	item, err := a.CreateDependentMedication(context.Background(), guardian.ID, dep.ID, api.CreateMedicationRequest{
		Name: "Losartana", Dose: "50mg", Instructions: "apos o cafe",
		Times: []string{"08:00", "20:00"}, Frequency: "daily",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if item.ID == 0 || item.Name != "Losartana" {
		t.Fatalf("unexpected item: %+v", item)
	}
	if !strings.Contains(item.Schedule, "8h") || !strings.Contains(item.Schedule, "20h") {
		t.Fatalf("schedule text missing hours: %q", item.Schedule)
	}

	list, err := a.ListDependentMedications(context.Background(), guardian.ID, dep.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != item.ID {
		t.Fatalf("list mismatch: %+v", list)
	}
}

func TestCreateDependentMedication_NotGuardian(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Estranho", "Joaquim")
	_, err := a.CreateDependentMedication(context.Background(), users[0].ID, users[1].ID, api.CreateMedicationRequest{
		Name: "X", Times: []string{"08:00"}, Frequency: "daily",
	})
	if !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestDeactivateDependentMedication(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Fabio", "Joaquim", "Outro")
	guardian, dep, other := users[0], users[1], users[2]
	if _, err := db.LinkFamily(guardian.ID, dep.ID, "Pai"); err != nil {
		t.Fatalf("link: %v", err)
	}
	item, err := a.CreateDependentMedication(context.Background(), guardian.ID, dep.ID, api.CreateMedicationRequest{
		Name: "Losartana", Times: []string{"08:00"}, Frequency: "daily",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Med de outro usuario -> ErrNotFound (nao vaza existencia).
	if _, err := db.LinkFamily(guardian.ID, other.ID, "Mae"); err != nil {
		t.Fatalf("link other: %v", err)
	}
	if err := a.DeactivateDependentMedication(context.Background(), guardian.ID, other.ID, item.ID); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("cross-dependent delete err = %v, want ErrNotFound", err)
	}

	// Delete correto.
	if err := a.DeactivateDependentMedication(context.Background(), guardian.ID, dep.ID, item.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	list, _ := a.ListDependentMedications(context.Background(), guardian.ID, dep.ID)
	if len(list) != 0 {
		t.Fatalf("expected empty after deactivate, got %d", len(list))
	}
}

func TestProfileFacts_AggregatesRelationsPeopleTrips(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Maria", "Fabio")
	maria, fabio := users[0], users[1]
	// Maria eh guardia de Fabio (dependente).
	if _, err := db.LinkFamily(maria.ID, fabio.ID, "filho"); err != nil {
		t.Fatalf("link: %v", err)
	}
	// Memoria social (pessoa) + memoria de risco (deve ser ocultada).
	_ = db.SaveMemory(maria.ID, "social_context", "dr_roberto", "cardiologista")
	_ = db.SaveMemory(maria.ID, "risco:queda_recente", "caiu no banheiro", "x")
	// Viagem futura.
	tp := &TravelPeriod{
		UserID:       maria.ID,
		StartDate:    mustDate(t, "2026-06-10"),
		EndDate:      mustDate(t, "2026-06-20"),
		Timezone:     "Europe/Paris",
		LocationName: "Paris",
	}
	if err := db.CreateTravelPeriod(tp); err != nil {
		t.Fatalf("travel: %v", err)
	}

	facts, err := a.ProfileFacts(context.Background(), maria.ID)
	if err != nil {
		t.Fatalf("ProfileFacts: %v", err)
	}
	if !facts.Available {
		t.Fatalf("expected available=true")
	}
	if len(facts.Relations) != 1 || facts.Relations[0].Kind != "dependent" {
		t.Fatalf("relations: %+v", facts.Relations)
	}
	if len(facts.People) != 1 || facts.People[0].Detail != "cardiologista" {
		t.Fatalf("people: %+v", facts.People)
	}
	// Memoria de risco NUNCA aparece.
	for _, p := range facts.People {
		if strings.Contains(strings.ToLower(p.Name), "queda") || strings.Contains(p.Detail, "banheiro") {
			t.Fatalf("risk memo leaked into people: %+v", p)
		}
	}
	if len(facts.Trips) != 1 || facts.Trips[0].Destination != "Paris" {
		t.Fatalf("trips: %+v", facts.Trips)
	}
}

// TestProfileFacts_RelacaoNotDuplicated blinda o bug de uma memoria "relacao"
// aparecer duas vezes — uma em Relations e outra em People — porque o loop de
// People incluia "relacao". Deve aparecer SO em Relations.
func TestProfileFacts_RelacaoNotDuplicated(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Waldyr")
	waldyr := users[0]
	_ = db.SaveMemory(waldyr.ID, "relacao", "sobrinha_maria_paula", "Maria Paula - sobrinha, aniversário 13/06")

	facts, err := a.ProfileFacts(context.Background(), waldyr.ID)
	if err != nil {
		t.Fatalf("ProfileFacts: %v", err)
	}
	if len(facts.Relations) != 1 {
		t.Fatalf("relations = %d, want 1: %+v", len(facts.Relations), facts.Relations)
	}
	for _, p := range facts.People {
		if strings.Contains(strings.ToLower(p.Name), "maria") {
			t.Fatalf("memoria 'relacao' vazou pra People (duplicata): %+v", p)
		}
	}
	if len(facts.People) != 0 {
		t.Fatalf("people = %d, want 0 (relacao nao deve entrar em people)", len(facts.People))
	}
}

// TestPersonFact_CreateRoundTripsToProfileFacts garante que o que a UI cadastra
// (CreatePersonFact) aparece editavel em ProfileFacts com a (category, key)
// crua, e que o bucket relacao/pessoa cai na secao certa.
func TestPersonFact_CreateRoundTripsToProfileFacts(t *testing.T) {
	a, db, _ := mkAdapter(t)
	u := mkUsers(t, db, "Giovanni")[0]
	ctx := context.Background()

	if err := a.CreatePersonFact(ctx, u.ID, api.PersonFactRequest{
		Name: "João Victor", Detail: "Colega da Octalab", Type: api.PersonFactTypePessoa,
	}); err != nil {
		t.Fatalf("create pessoa: %v", err)
	}
	if err := a.CreatePersonFact(ctx, u.ID, api.PersonFactRequest{
		Name: "Fábio", Detail: "Pai", Type: api.PersonFactTypeRelacao,
	}); err != nil {
		t.Fatalf("create relacao: %v", err)
	}

	facts, err := a.ProfileFacts(ctx, u.ID)
	if err != nil {
		t.Fatalf("ProfileFacts: %v", err)
	}
	if len(facts.Relations) != 1 || !facts.Relations[0].Editable ||
		facts.Relations[0].Category != "relacao" || facts.Relations[0].Key != "Fábio" {
		t.Fatalf("relation editable/identity errada: %+v", facts.Relations)
	}
	if len(facts.People) != 1 || !facts.People[0].Editable ||
		facts.People[0].Category != "social_context" || facts.People[0].Key != "João Victor" {
		t.Fatalf("person editable/identity errada: %+v", facts.People)
	}
}

// TestPersonFact_FamilyLinkNotEditable garante que vinculos familiares vem como
// nao-editaveis (geridos nas telas de familia), sem category/key.
func TestPersonFact_FamilyLinkNotEditable(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Maria", "Fabio")
	if _, err := db.LinkFamily(users[0].ID, users[1].ID, "filho"); err != nil {
		t.Fatalf("link: %v", err)
	}
	facts, err := a.ProfileFacts(context.Background(), users[0].ID)
	if err != nil {
		t.Fatalf("ProfileFacts: %v", err)
	}
	if len(facts.Relations) != 1 {
		t.Fatalf("relations = %d, want 1", len(facts.Relations))
	}
	if facts.Relations[0].Editable || facts.Relations[0].Key != "" {
		t.Fatalf("vinculo familiar nao deveria ser editavel: %+v", facts.Relations[0])
	}
}

// TestPersonFact_UpdateRenameRemovesOldMemory valida o rename atomico: a chave
// antiga some e a nova carrega o valor.
func TestPersonFact_UpdateRenameRemovesOldMemory(t *testing.T) {
	a, db, _ := mkAdapter(t)
	u := mkUsers(t, db, "Giovanni")[0]
	ctx := context.Background()
	_ = db.SaveMemory(u.ID, "social_context", "jo", "colega")

	if err := a.UpdatePersonFact(ctx, u.ID, api.PersonFactRequest{
		Name: "João", Detail: "colega de trabalho", Type: api.PersonFactTypePessoa,
		OriginalCategory: "social_context", OriginalKey: "jo",
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if exists, _ := db.MemoryExists(u.ID, "social_context", "jo"); exists {
		t.Fatalf("chave antiga 'jo' deveria ter sumido")
	}
	mems, _ := db.GetMemories(u.ID, "social_context")
	if len(mems) != 1 || mems[0].Key != "João" || mems[0].Value != "colega de trabalho" {
		t.Fatalf("memoria renomeada errada: %+v", mems)
	}
}

// TestPersonFact_DeleteRemovesMemory valida a remocao via adapter.
func TestPersonFact_DeleteRemovesMemory(t *testing.T) {
	a, db, _ := mkAdapter(t)
	u := mkUsers(t, db, "Giovanni")[0]
	ctx := context.Background()
	_ = db.SaveMemory(u.ID, "relacao", "Fábio", "Pai")

	if err := a.DeletePersonFact(ctx, u.ID, "relacao", "Fábio"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if exists, _ := db.MemoryExists(u.ID, "relacao", "Fábio"); exists {
		t.Fatalf("memoria deveria ter sido removida")
	}
}

func TestProfileFacts_EmptyAvailableFalse(t *testing.T) {
	a, db, _ := mkAdapter(t)
	u := mkUsers(t, db, "Solo")[0]
	facts, err := a.ProfileFacts(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ProfileFacts: %v", err)
	}
	if facts.Available {
		t.Fatalf("expected available=false for empty")
	}
	if facts.Relations == nil || facts.People == nil || facts.Trips == nil {
		t.Fatalf("slices must be non-nil: %+v", facts)
	}
}

func TestActivityHistory_FiltersAndOrders(t *testing.T) {
	a, db, _ := mkAdapter(t)
	u := mkUsers(t, db, "Maria")[0]
	base := mustDate(t, "2026-05-20")
	insertActionLog(t, db, u.ID, "consultar_agenda", base) // ruido
	insertActionLog(t, db, u.ID, "criar_evento", base.Add(60_000_000_000))
	insertActionLog(t, db, u.ID, "synthesis_executed", base.Add(120_000_000_000)) // ruido
	insertActionLog(t, db, u.ID, "medication_taken", base.Add(180_000_000_000))

	items, err := a.ActivityHistory(context.Background(), u.ID, 100)
	if err != nil {
		t.Fatalf("ActivityHistory: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2 relevantes", len(items))
	}
	if items[0].Action != "medication_taken" {
		t.Fatalf("first = %q, want medication_taken (desc)", items[0].Action)
	}
}

// startFixed eh um dia de inicio estavel em BRT pra deterministica.
func startFixedMed() time.Time {
	return time.Date(2026, 6, 1, 9, 0, 0, 0, BRT()) // 01/06/2026 09:00
}

func TestResolveMedicationEndDate_Continuous(t *testing.T) {
	for i, d := range []*api.MedicationDuration{nil, {Kind: "continuous"}, {Kind: ""}} {
		got, err := resolveMedicationEndDate(startFixedMed(), d)
		if err != nil {
			t.Fatalf("case %d: unexpected err: %v", i, err)
		}
		if got != nil {
			t.Fatalf("case %d: expected nil end (continuous), got %v", i, got)
		}
	}
}

func TestResolveMedicationEndDate_Period(t *testing.T) {
	tests := []struct {
		name, unit, want string
		count            int
	}{
		{"3 dias", "days", "2026-06-03", 3},
		{"1 dia", "days", "2026-06-01", 1},
		{"3 semanas", "weeks", "2026-06-21", 3},
		{"1 mes", "months", "2026-06-30", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveMedicationEndDate(startFixedMed(), &api.MedicationDuration{
				Kind: "period", Count: tt.count, Unit: tt.unit,
			})
			if err != nil {
				t.Fatalf("err: %v", err)
			}
			if got == nil || got.Format("2006-01-02") != tt.want {
				t.Fatalf("end = %v, want %s", got, tt.want)
			}
		})
	}
}

func TestResolveMedicationEndDate_Invalid(t *testing.T) {
	bad := []*api.MedicationDuration{
		{Kind: "period", Count: 0, Unit: "days"},
		{Kind: "period", Count: 3, Unit: "anos"},
		{Kind: "period", Count: 400, Unit: "days"},
		{Kind: "until", Until: "2026-05-30"}, // passado
		{Kind: "until", Until: "xx"},         // formato invalido
		{Kind: "qualquer"},                   // kind desconhecido
	}
	for i, d := range bad {
		if _, err := resolveMedicationEndDate(startFixedMed(), d); err == nil {
			t.Fatalf("case %d: expected error, got nil", i)
		}
	}
}

func TestCreateAndListMyMedication_Temporary(t *testing.T) {
	a, db, _ := mkAdapter(t)
	u := mkUsers(t, db, "Giovanni")[0]

	// Tratamento por 3 semanas a partir de hoje.
	item, err := a.CreateMyMedication(context.Background(), u.ID, api.CreateMedicationRequest{
		Name: "Amoxicilina", Dose: "500mg", Times: []string{"08:00", "20:00"},
		Frequency: "daily",
		Duration:  &api.MedicationDuration{Kind: "period", Count: 3, Unit: "weeks"},
	})
	if err != nil {
		t.Fatalf("CreateMyMedication: %v", err)
	}
	if item.EndsAt == nil {
		t.Fatalf("expected ends_at on temporary medication, got nil")
	}
	if !strings.Contains(item.Schedule, "até") {
		t.Fatalf("schedule should mention término: %q", item.Schedule)
	}

	// Continuo: sem ends_at.
	cont, err := a.CreateMyMedication(context.Background(), u.ID, api.CreateMedicationRequest{
		Name: "Losartana", Dose: "50mg", Times: []string{"08:00"}, Frequency: "daily",
	})
	if err != nil {
		t.Fatalf("create continuous: %v", err)
	}
	if cont.EndsAt != nil {
		t.Fatalf("continuous med should have nil ends_at, got %v", *cont.EndsAt)
	}

	list, err := a.ListMyMedications(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("ListMyMedications: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 meds, got %d", len(list))
	}
}

func TestDeactivateMyMedication_OnlyOwner(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Dono", "Outro")
	owner, other := users[0], users[1]

	item, err := a.CreateMyMedication(context.Background(), owner.ID, api.CreateMedicationRequest{
		Name: "X", Times: []string{"08:00"}, Frequency: "daily",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Outro usuario nao pode desativar o remedio do dono.
	if err := a.DeactivateMyMedication(context.Background(), other.ID, item.ID); !errors.Is(err, api.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-owner, got %v", err)
	}
	// Dono desativa normalmente.
	if err := a.DeactivateMyMedication(context.Background(), owner.ID, item.ID); err != nil {
		t.Fatalf("owner deactivate: %v", err)
	}
	list, _ := a.ListMyMedications(context.Background(), owner.ID)
	if len(list) != 0 {
		t.Fatalf("expected 0 active after deactivate, got %d", len(list))
	}
}
