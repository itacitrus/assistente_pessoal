package main

import (
	"testing"
	"time"
)

func TestManualRefresh_OncePerDay(t *testing.T) {
	db := setupTestDB(t)
	u := makeElder(t, db, "Antonia", "111")
	scope := "insights"

	todayMidnight := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	tomorrowMidnight := todayMidnight.Add(24 * time.Hour)

	// Sem registro: permitido.
	ok, err := db.ManualRefreshAllowed(u.ID, scope, todayMidnight)
	if err != nil || !ok {
		t.Fatalf("inicial: ok=%v err=%v (queria true)", ok, err)
	}

	// Marca uso hoje.
	now := todayMidnight.Add(10 * time.Hour)
	if err := db.MarkManualRefresh(u.ID, scope, now); err != nil {
		t.Fatalf("mark: %v", err)
	}

	// Mesmo dia: bloqueado (last >= meia-noite de hoje).
	if ok, _ := db.ManualRefreshAllowed(u.ID, scope, todayMidnight); ok {
		t.Error("mesmo dia deveria bloquear")
	}

	// Dia seguinte: liberado (last < meia-noite de amanha).
	if ok, _ := db.ManualRefreshAllowed(u.ID, scope, tomorrowMidnight); !ok {
		t.Error("dia seguinte deveria liberar")
	}

	// Escopo diferente nao eh afetado pelo limite de outro escopo.
	if ok, _ := db.ManualRefreshAllowed(u.ID, "dependent:9", todayMidnight); !ok {
		t.Error("escopo diferente deveria liberar")
	}
}

func TestAlertSafeSummary(t *testing.T) {
	cases := []struct {
		name        string
		policy      string
		details     string
		wantSummary string
		wantRec     string
	}{
		{
			name:        "safety_net expoe reason+recommended",
			policy:      "severe_signal_safety_net",
			details:     "severity=warn|category=psicologico|reason=demonstrou tristeza persistente|recommended=considere ligar hoje|date=2026-05-25|via=writer_safety_net",
			wantSummary: "demonstrou tristeza persistente",
			wantRec:     "considere ligar hoje",
		},
		{
			name:        "companion usa recommended_action",
			policy:      "severe_signal",
			details:     "severity=critical|category=psicologico|reason=mencao a auto-lesao|recommended_action=procure contato imediato",
			wantSummary: "mencao a auto-lesao",
			wantRec:     "procure contato imediato",
		},
		{
			name:        "medicacao nao expoe detalhe",
			policy:      "medication_miss",
			details:     "med_id=5|reason=esqueceu",
			wantSummary: "",
			wantRec:     "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, rec := alertSafeSummary(c.policy, c.details)
			if s != c.wantSummary {
				t.Errorf("summary = %q, want %q", s, c.wantSummary)
			}
			if rec != c.wantRec {
				t.Errorf("recommended = %q, want %q", rec, c.wantRec)
			}
		})
	}
}
