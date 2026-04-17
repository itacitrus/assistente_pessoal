package main

import (
	"fmt"
	"time"
)

type DateSource string

const (
	DateSourceExplicit DateSource = "explicit"
	DateSourceInferred DateSource = "inferred"
)

type ResolveInput struct {
	Source       DateSource
	ExplicitDate string // "YYYY-MM-DD", obrigatorio se Source=Explicit
	Time         string // "HH:MM"
	Now          time.Time
	Loc          *time.Location
}

type ResolveOutput struct {
	Start      time.Time
	Adjusted   bool
	AdjustNote string
}

func ResolveEventDate(in ResolveInput) (ResolveOutput, error) {
	if in.Loc == nil {
		return ResolveOutput{}, fmt.Errorf("Loc obrigatorio")
	}
	hh, mm, err := parseHHMM(in.Time)
	if err != nil {
		return ResolveOutput{}, err
	}
	nowInLoc := in.Now.In(in.Loc)

	switch in.Source {
	case DateSourceInferred:
		today := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), hh, mm, 0, 0, in.Loc)
		if today.After(nowInLoc) {
			return ResolveOutput{Start: today}, nil
		}
		return ResolveOutput{Start: today.AddDate(0, 0, 1)}, nil

	case DateSourceExplicit:
		d, err := time.ParseInLocation("2006-01-02", in.ExplicitDate, in.Loc)
		if err != nil {
			return ResolveOutput{}, fmt.Errorf("ExplicitDate invalido %q: %w", in.ExplicitDate, err)
		}
		candidate := time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, in.Loc)
		today := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), 0, 0, 0, 0, in.Loc)
		eventDay := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, in.Loc)
		if eventDay.Equal(today) && !candidate.After(nowInLoc) {
			return ResolveOutput{
				Start:      candidate.AddDate(0, 0, 1),
				Adjusted:   true,
				AdjustNote: "Esse horario ja passou hoje. Marquei pra amanha nesse horario.",
			}, nil
		}
		if eventDay.Before(today) {
			return ResolveOutput{}, fmt.Errorf("data explicita no passado: %s", in.ExplicitDate)
		}
		return ResolveOutput{Start: candidate}, nil

	default:
		return ResolveOutput{}, fmt.Errorf("date_source invalido: %q", in.Source)
	}
}

func parseHHMM(s string) (int, int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("time invalido %q: %w", s, err)
	}
	return t.Hour(), t.Minute(), nil
}
