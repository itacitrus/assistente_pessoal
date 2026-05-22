package main

import (
	"errors"
	"testing"
)

// mkSched monta um schedule simples pra um RRULE.
func dupSched(rrule string) *MedicationSchedule {
	return &MedicationSchedule{RRULE: rrule}
}

// TestCreateMedicationWithSchedule_BlocksIdenticalCopy garante a trava: nome +
// dose + horario (RRULE) iguais num remedio ja ativo do mesmo dono recusam o
// segundo cadastro com ErrMedicationDuplicate, sem criar nada.
func TestCreateMedicationWithSchedule_BlocksIdenticalCopy(t *testing.T) {
	db := setupTestDB(t)
	u := mkUsers(t, db, "Idoso")[0]

	rrule := "FREQ=DAILY;BYHOUR=21;BYMINUTE=29"
	first := &Medication{UserID: u.ID, Name: "Prednisona", Dose: "20mg"}
	if err := db.CreateMedicationWithSchedule(first, dupSched(rrule)); err != nil {
		t.Fatalf("first create: %v", err)
	}

	dup := &Medication{UserID: u.ID, Name: "Prednisona", Dose: "20mg"}
	err := db.CreateMedicationWithSchedule(dup, dupSched(rrule))
	if !errors.Is(err, ErrMedicationDuplicate) {
		t.Fatalf("expected ErrMedicationDuplicate, got %v", err)
	}

	meds, err := db.ListActiveMedications(u.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(meds) != 1 {
		t.Fatalf("expected 1 active med (duplicate rejected), got %d", len(meds))
	}
}

// TestCreateMedicationWithSchedule_AllowsDifferentDoseOrTime confirma que a
// trava NAO bloqueia desmame (mesmo nome, dose diferente) nem mesmo nome+dose
// com horario diferente — so copia 100% identica e barrada.
func TestCreateMedicationWithSchedule_AllowsDifferentDoseOrTime(t *testing.T) {
	db := setupTestDB(t)
	u := mkUsers(t, db, "Idoso")[0]

	base := "FREQ=DAILY;BYHOUR=8;BYMINUTE=0"
	if err := db.CreateMedicationWithSchedule(
		&Medication{UserID: u.ID, Name: "Prednisona", Dose: "20mg"}, dupSched(base)); err != nil {
		t.Fatalf("create 20mg: %v", err)
	}
	// Desmame: mesmo nome, dose diferente -> permitido.
	if err := db.CreateMedicationWithSchedule(
		&Medication{UserID: u.ID, Name: "Prednisona", Dose: "10mg"}, dupSched(base)); err != nil {
		t.Fatalf("taper (different dose) should be allowed: %v", err)
	}
	// Mesmo nome+dose, horario diferente -> permitido.
	if err := db.CreateMedicationWithSchedule(
		&Medication{UserID: u.ID, Name: "Prednisona", Dose: "20mg"},
		dupSched("FREQ=DAILY;BYHOUR=20;BYMINUTE=0")); err != nil {
		t.Fatalf("different time should be allowed: %v", err)
	}

	meds, err := db.ListActiveMedications(u.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(meds) != 3 {
		t.Fatalf("expected 3 active meds, got %d", len(meds))
	}
}

// TestDuplicateActiveMedicationExists_ExcludesSelf garante que a checagem de
// edicao ignora o proprio remedio (excludeID), senao toda edicao que mantem o
// horario falsaria um "duplicado".
func TestDuplicateActiveMedicationExists_ExcludesSelf(t *testing.T) {
	db := setupTestDB(t)
	u := mkUsers(t, db, "Idoso")[0]

	rrule := "FREQ=DAILY;BYHOUR=8;BYMINUTE=0"
	m := &Medication{UserID: u.ID, Name: "Losartana", Dose: "50mg"}
	if err := db.CreateMedicationWithSchedule(m, dupSched(rrule)); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Excluindo o proprio id: nao deve achar duplicata.
	dup, err := db.DuplicateActiveMedicationExists(u.ID, m.ID, "Losartana", "50mg", rrule)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if dup {
		t.Fatal("editing self should not be a duplicate")
	}

	// Sem excluir (excludeID=0): acha a si mesmo como duplicata.
	dup, err = db.DuplicateActiveMedicationExists(u.ID, 0, "Losartana", "50mg", rrule)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if !dup {
		t.Fatal("expected duplicate when not excluding self")
	}
}
