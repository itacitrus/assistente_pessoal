"use client";

import * as React from "react";
import {
  CalendarClock,
  ChevronLeft,
  ChevronRight,
  MapPin,
} from "lucide-react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { ApiError } from "@/lib/api";
import { getMyAgendaEvents } from "@/lib/api/me";
import { cn } from "@/lib/utils";
import { APP_TIME_ZONE as TZ } from "@/lib/format";
import type { AgendaEvent } from "@/types/api";
const WEEKDAY_LABELS = ["Dom", "Seg", "Ter", "Qua", "Qui", "Sex", "Sáb"];
const MONTH_LABELS = [
  "janeiro",
  "fevereiro",
  "março",
  "abril",
  "maio",
  "junho",
  "julho",
  "agosto",
  "setembro",
  "outubro",
  "novembro",
  "dezembro",
];

/** YYYY-MM-DD de uma data tratada como dia de calendário (sem fuso). */
function ymd(d: Date): string {
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

/** Day-key (YYYY-MM-DD) de um instante ISO, no fuso de Brasília. */
const dayKeyFmt = new Intl.DateTimeFormat("en-CA", {
  timeZone: TZ,
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
});
function eventDayKey(iso: string): string {
  return dayKeyFmt.format(new Date(iso));
}

const timeFmt = new Intl.DateTimeFormat("pt-BR", {
  timeZone: TZ,
  hour: "2-digit",
  minute: "2-digit",
});
function eventTime(ev: AgendaEvent): string {
  if (ev.all_day) return "Dia inteiro";
  return timeFmt.format(new Date(ev.start));
}

/** Primeiro dia da grade: domingo da semana que contém o dia 1 do mês. */
function gridStart(viewYear: number, viewMonth: number): Date {
  const first = new Date(viewYear, viewMonth, 1);
  const d = new Date(first);
  d.setDate(1 - first.getDay()); // recua até domingo
  return d;
}

export function AgendaCalendar() {
  const today = React.useMemo(() => new Date(), []);
  const todayKey = ymd(today);

  const [view, setView] = React.useState({
    year: today.getFullYear(),
    month: today.getMonth(),
  });
  const [selected, setSelected] = React.useState<string>(todayKey);
  const [events, setEvents] = React.useState<AgendaEvent[]>([]);
  const [connected, setConnected] = React.useState(true);
  const [status, setStatus] = React.useState<"loading" | "ok" | "error">(
    "loading",
  );
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  // 42 células (6 semanas) a partir do domingo inicial.
  const cells = React.useMemo(() => {
    const start = gridStart(view.year, view.month);
    return Array.from({ length: 42 }, (_, i) => {
      const d = new Date(start);
      d.setDate(start.getDate() + i);
      return d;
    });
  }, [view]);

  React.useEffect(() => {
    let cancelled = false;
    setStatus("loading");
    setErrorMsg(null);
    // Busca com 1 dia de folga de cada lado pra cobrir o skew de fuso (BRT=UTC-3)
    // ao agrupar por dia de Brasília.
    const from = new Date(cells[0]);
    from.setDate(from.getDate() - 1);
    const to = new Date(cells[cells.length - 1]);
    to.setDate(to.getDate() + 2); // exclusivo + folga
    getMyAgendaEvents(ymd(from), ymd(to))
      .then((res) => {
        if (cancelled) return;
        setConnected(res.google_connected);
        setEvents(res.events ?? []);
        setStatus("ok");
      })
      .catch((err) => {
        if (cancelled) return;
        setStatus("error");
        setErrorMsg(
          err instanceof ApiError
            ? err.message
            : "Não consegui carregar os eventos agora.",
        );
      });
    return () => {
      cancelled = true;
    };
  }, [cells]);

  const byDay = React.useMemo(() => {
    const map = new Map<string, AgendaEvent[]>();
    for (const ev of events) {
      const key = eventDayKey(ev.start);
      const arr = map.get(key);
      if (arr) arr.push(ev);
      else map.set(key, [ev]);
    }
    return map;
  }, [events]);

  const selectedEvents = byDay.get(selected) ?? [];

  function goMonth(delta: number) {
    setView((v) => {
      const d = new Date(v.year, v.month + delta, 1);
      return { year: d.getFullYear(), month: d.getMonth() };
    });
  }
  function goToday() {
    setView({ year: today.getFullYear(), month: today.getMonth() });
    setSelected(todayKey);
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="font-display text-xl font-semibold capitalize tracking-tight">
          {MONTH_LABELS[view.month]} de {view.year}
        </h2>
        <div className="flex items-center gap-1">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={goToday}
            className="mr-1"
          >
            Hoje
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label="Mês anterior"
            onClick={() => goMonth(-1)}
          >
            <ChevronLeft className="h-4 w-4" aria-hidden />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            aria-label="Próximo mês"
            onClick={() => goMonth(1)}
          >
            <ChevronRight className="h-4 w-4" aria-hidden />
          </Button>
        </div>
      </div>

      {!connected && status === "ok" ? (
        <Alert>
          <AlertDescription>
            Conecte sua agenda do Google no painel para ver seus compromissos
            aqui.
          </AlertDescription>
        </Alert>
      ) : null}
      {errorMsg ? (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      ) : null}

      <Card className="overflow-hidden shadow-warm">
        <CardContent className="p-0">
          {/* Cabeçalho de dias da semana */}
          <div className="grid grid-cols-7 border-b border-border/70 bg-muted/30">
            {WEEKDAY_LABELS.map((w) => (
              <div
                key={w}
                className="py-2 text-center text-xs font-medium text-muted-foreground"
              >
                {w}
              </div>
            ))}
          </div>
          {/* Grade do mês */}
          <div className="grid grid-cols-7">
            {cells.map((d, i) => {
              const key = ymd(d);
              const inMonth = d.getMonth() === view.month;
              const isToday = key === todayKey;
              const isSelected = key === selected;
              const dayEvents = byDay.get(key) ?? [];
              return (
                <button
                  type="button"
                  key={key}
                  onClick={() => setSelected(key)}
                  className={cn(
                    "min-h-[64px] border-b border-r border-border/50 p-1.5 text-left align-top transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                    i % 7 === 6 && "border-r-0",
                    !inMonth && "bg-muted/20 text-muted-foreground",
                    isSelected && "bg-[--zello-emerald]/10",
                    "hover:bg-accent",
                  )}
                  aria-pressed={isSelected}
                  aria-label={`${d.getDate()} — ${dayEvents.length} evento(s)`}
                >
                  <span
                    className={cn(
                      "inline-flex h-6 w-6 items-center justify-center rounded-full text-sm",
                      isToday &&
                        "bg-[--zello-emerald] font-semibold text-primary-foreground",
                      !isToday && inMonth && "text-foreground",
                    )}
                  >
                    {d.getDate()}
                  </span>
                  <div className="mt-1 space-y-0.5">
                    {dayEvents.slice(0, 2).map((ev) => (
                      <div
                        key={ev.id}
                        className="truncate rounded bg-[--zello-emerald]/15 px-1 py-0.5 text-[11px] leading-tight text-[--zello-emerald-deep]"
                        title={ev.title}
                      >
                        {ev.all_day ? "" : `${eventTime(ev)} `}
                        {ev.title}
                      </div>
                    ))}
                    {dayEvents.length > 2 ? (
                      <div className="px-1 text-[11px] text-muted-foreground">
                        +{dayEvents.length - 2}
                      </div>
                    ) : null}
                  </div>
                </button>
              );
            })}
          </div>
        </CardContent>
      </Card>

      {/* Painel do dia selecionado */}
      <DaySchedule
        dateKey={selected}
        events={selectedEvents}
        loading={status === "loading"}
      />
    </div>
  );
}

const longDateFmt = new Intl.DateTimeFormat("pt-BR", {
  timeZone: TZ,
  weekday: "long",
  day: "2-digit",
  month: "long",
});

function DaySchedule({
  dateKey,
  events,
  loading,
}: {
  dateKey: string;
  events: AgendaEvent[];
  loading: boolean;
}) {
  // dateKey é "YYYY-MM-DD" de Brasília; monta um Date ao meio-dia pra rotular
  // sem risco de virar o dia por fuso.
  const label = React.useMemo(() => {
    const [y, m, d] = dateKey.split("-").map(Number);
    return longDateFmt.format(new Date(y, m - 1, d, 12));
  }, [dateKey]);

  return (
    <Card className="shadow-warm">
      <CardContent className="p-5">
        <p className="mb-3 font-medium capitalize text-foreground">{label}</p>
        {loading ? (
          <p className="text-sm text-muted-foreground">Carregando…</p>
        ) : events.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            Nenhum compromisso neste dia.
          </p>
        ) : (
          <ul className="divide-y divide-border/70">
            {events.map((ev) => (
              <li key={ev.id} className="flex gap-3 py-3 first:pt-0 last:pb-0">
                <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-[--zello-emerald]/10 text-[--zello-emerald]">
                  <CalendarClock className="h-4 w-4" aria-hidden />
                </div>
                <div className="min-w-0">
                  <p className="font-medium text-foreground">{ev.title}</p>
                  <p className="text-sm text-muted-foreground">
                    {eventTime(ev)}
                  </p>
                  {ev.location ? (
                    <p className="mt-0.5 flex items-center gap-1 text-sm text-muted-foreground">
                      <MapPin className="h-3.5 w-3.5 shrink-0" aria-hidden />
                      <span className="truncate">{ev.location}</span>
                    </p>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
