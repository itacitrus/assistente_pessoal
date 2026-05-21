package main

import (
	"context"
	"errors"
	"strings"
	"testing"

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
