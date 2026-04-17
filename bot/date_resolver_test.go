package main

import (
	"testing"
	"time"
)

func TestResolveEventDate_Inferred(t *testing.T) {
	brt, _ := time.LoadLocation("America/Sao_Paulo")
	now0702 := time.Date(2026, 4, 16, 7, 2, 0, 0, brt)
	now2345 := time.Date(2026, 4, 16, 23, 45, 0, 0, brt)

	tests := []struct {
		name     string
		now      time.Time
		time     string
		wantDate time.Time
	}{
		{
			name:     "hora > agora resolve para hoje (regressao do bug OTC)",
			now:      now0702,
			time:     "09:00",
			wantDate: time.Date(2026, 4, 16, 9, 0, 0, 0, brt),
		},
		{
			name:     "hora < agora resolve para amanha (5h da manha)",
			now:      now0702,
			time:     "05:00",
			wantDate: time.Date(2026, 4, 17, 5, 0, 0, 0, brt),
		},
		{
			name:     "PM-default ja aplicado por Claude: 17:00 resolve para hoje",
			now:      now0702,
			time:     "17:00",
			wantDate: time.Date(2026, 4, 16, 17, 0, 0, 0, brt),
		},
		{
			name:     "time == now resolve para amanha",
			now:      now0702,
			time:     "07:02",
			wantDate: time.Date(2026, 4, 17, 7, 2, 0, 0, brt),
		},
		{
			name:     "travessia de meia-noite: 23:30 sendo 23:45 vai pra amanha",
			now:      now2345,
			time:     "23:30",
			wantDate: time.Date(2026, 4, 17, 23, 30, 0, 0, brt),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveEventDate(ResolveInput{
				Source: DateSourceInferred,
				Time:   tc.time,
				Now:    tc.now,
				Loc:    brt,
			})
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if !got.Start.Equal(tc.wantDate) {
				t.Fatalf("Start = %s, queria %s", got.Start, tc.wantDate)
			}
			if got.Adjusted {
				t.Fatalf("Adjusted esperava false para inferred, deu true")
			}
		})
	}
}

func TestResolveEventDate_Explicit(t *testing.T) {
	brt, _ := time.LoadLocation("America/Sao_Paulo")
	now0702 := time.Date(2026, 4, 16, 7, 2, 0, 0, brt)

	t.Run("explicit data futura sem ajuste", func(t *testing.T) {
		got, err := ResolveEventDate(ResolveInput{
			Source:       DateSourceExplicit,
			ExplicitDate: "2026-04-20",
			Time:         "14:00",
			Now:          now0702,
			Loc:          brt,
		})
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		want := time.Date(2026, 4, 20, 14, 0, 0, 0, brt)
		if !got.Start.Equal(want) {
			t.Fatalf("Start = %s, queria %s", got.Start, want)
		}
		if got.Adjusted {
			t.Fatalf("Adjusted deveria ser false")
		}
	})

	t.Run("explicit hoje com hora no futuro sem ajuste", func(t *testing.T) {
		got, err := ResolveEventDate(ResolveInput{
			Source:       DateSourceExplicit,
			ExplicitDate: "2026-04-16",
			Time:         "09:00",
			Now:          now0702,
			Loc:          brt,
		})
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		want := time.Date(2026, 4, 16, 9, 0, 0, 0, brt)
		if !got.Start.Equal(want) {
			t.Fatalf("Start = %s, queria %s", got.Start, want)
		}
		if got.Adjusted {
			t.Fatalf("Adjusted deveria ser false")
		}
	})

	t.Run("explicit hoje com hora passada: auto-ajusta para amanha", func(t *testing.T) {
		got, err := ResolveEventDate(ResolveInput{
			Source:       DateSourceExplicit,
			ExplicitDate: "2026-04-16",
			Time:         "05:00",
			Now:          now0702,
			Loc:          brt,
		})
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		want := time.Date(2026, 4, 17, 5, 0, 0, 0, brt)
		if !got.Start.Equal(want) {
			t.Fatalf("Start = %s, queria %s", got.Start, want)
		}
		if !got.Adjusted {
			t.Fatalf("Adjusted deveria ser true")
		}
		if got.AdjustNote == "" {
			t.Fatalf("AdjustNote deveria ser preenchido")
		}
	})

	t.Run("explicit data passada retorna erro", func(t *testing.T) {
		_, err := ResolveEventDate(ResolveInput{
			Source:       DateSourceExplicit,
			ExplicitDate: "2026-04-10",
			Time:         "09:00",
			Now:          now0702,
			Loc:          brt,
		})
		if err == nil {
			t.Fatalf("esperava erro, deu nil")
		}
	})
}
