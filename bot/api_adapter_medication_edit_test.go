package main

import (
	"context"
	"testing"

	"github.com/giovannirambo/assistente_pessoal/bot/api"
)

// Edicao de medicamento do dependente: substitui campos + schedule e devolve
// os campos estruturados pre-preenchiveis.
func TestUpdateDependentMedication_ReplacesFieldsAndSchedule(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Guardiao", "Idoso")
	guardian, dep := users[0], users[1]
	if _, err := db.LinkFamily(guardian.ID, dep.ID, "filho"); err != nil {
		t.Fatal(err)
	}
	created, err := a.CreateDependentMedication(context.Background(), guardian.ID, dep.ID, api.CreateMedicationRequest{
		Name: "Losartana", Dose: "50mg", Times: []string{"08:00"}, Frequency: "daily",
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := a.UpdateDependentMedication(context.Background(), guardian.ID, dep.ID, created.ID, api.CreateMedicationRequest{
		Name: "Losartana", Dose: "100mg", Times: []string{"09:00", "21:00"}, Frequency: "daily",
		ToleranceMinutes: 45, LateDosePolicy: "skip",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Dose != "100mg" {
		t.Fatalf("dose not updated: %q", updated.Dose)
	}
	if updated.ToleranceMinutes != 45 || updated.LateDosePolicy != "skip" {
		t.Fatalf("tolerance/policy not updated: %+v", updated)
	}
	if len(updated.Times) != 2 || updated.Times[0] != "09:00" || updated.Times[1] != "21:00" {
		t.Fatalf("structured times not replaced: %+v", updated.Times)
	}
}

// Edicao exige autorizacao: guardiao sem vinculo recebe ErrNotFound.
func TestUpdateDependentMedication_Unauthorized(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Guardiao", "Idoso", "Estranho")
	guardian, dep, stranger := users[0], users[1], users[2]
	if _, err := db.LinkFamily(guardian.ID, dep.ID, "filho"); err != nil {
		t.Fatal(err)
	}
	created, _ := a.CreateDependentMedication(context.Background(), guardian.ID, dep.ID, api.CreateMedicationRequest{
		Name: "X", Times: []string{"08:00"}, Frequency: "daily",
	})
	_, err := a.UpdateDependentMedication(context.Background(), stranger.ID, dep.ID, created.ID, api.CreateMedicationRequest{
		Name: "X", Times: []string{"09:00"}, Frequency: "daily",
	})
	if err != api.ErrNotFound {
		t.Fatalf("expected ErrNotFound for unauthorized, got %v", err)
	}
}

// Desvincular dependente remove o vinculo (reversivel) mantendo a conta.
func TestUnlinkDependent(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Guardiao", "Idoso")
	guardian, dep := users[0], users[1]
	if _, err := db.LinkFamily(guardian.ID, dep.ID, "filho"); err != nil {
		t.Fatal(err)
	}
	if err := a.UnlinkDependent(context.Background(), guardian.ID, dep.ID); err != nil {
		t.Fatal(err)
	}
	ok, _ := db.IsGuardianOf(guardian.ID, dep.ID)
	if ok {
		t.Fatal("link should be gone after unlink")
	}
	// A conta do idoso permanece.
	if _, err := db.GetUserByID(dep.ID); err != nil {
		t.Fatalf("dependent account should remain: %v", err)
	}
	// Desvincular sem vinculo -> ErrNotFound.
	if err := a.UnlinkDependent(context.Background(), guardian.ID, dep.ID); err != api.ErrNotFound {
		t.Fatalf("expected ErrNotFound on second unlink, got %v", err)
	}
}

// Desativar o dependente via patch pausa a conta (is_active=0), reversivel.
func TestUpdateDependent_DeactivateReactivate(t *testing.T) {
	a, db, _ := mkAdapter(t)
	users := mkUsers(t, db, "Guardiao", "Idoso")
	guardian, dep := users[0], users[1]
	if _, err := db.LinkFamily(guardian.ID, dep.ID, "filho"); err != nil {
		t.Fatal(err)
	}
	off := false
	if _, err := a.UpdateDependent(context.Background(), guardian.ID, dep.ID, api.DependentPatch{Active: &off}); err != nil {
		t.Fatal(err)
	}
	u, _ := db.GetUserByID(dep.ID)
	if u.IsActive {
		t.Fatal("dependent should be inactive after deactivate")
	}
	on := true
	if _, err := a.UpdateDependent(context.Background(), guardian.ID, dep.ID, api.DependentPatch{Active: &on}); err != nil {
		t.Fatal(err)
	}
	u, _ = db.GetUserByID(dep.ID)
	if !u.IsActive {
		t.Fatal("dependent should be active after reactivate")
	}
}
