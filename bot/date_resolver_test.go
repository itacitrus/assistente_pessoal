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
