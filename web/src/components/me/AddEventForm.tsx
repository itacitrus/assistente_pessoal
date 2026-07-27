"use client";

import * as React from "react";
import { CalendarPlus, Loader2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card, CardContent } from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ApiError } from "@/lib/api";
import { createMyAgendaEvent } from "@/lib/api/me";

export interface AddEventFormProps {
  /** Dia pré-selecionado (YYYY-MM-DD) — default do campo de data. */
  defaultDate: string;
  /** Chamado após criar, com o dia (YYYY-MM-DD) do novo evento. */
  onCreated: (dayKey: string) => void;
}

const DURATIONS = [
  { label: "30 min", value: 30 },
  { label: "1 hora", value: 60 },
  { label: "1h30", value: 90 },
  { label: "2 horas", value: 120 },
];

/**
 * Botão + formulário inline para adicionar um compromisso na agenda. Cria no
 * Google Calendar do titular (funciona no "ver como") e, opcionalmente, avisa
 * no WhatsApp. O projeto não tem primitivo de Dialog — form colapsável segue o
 * padrão da base.
 */
export function AddEventForm({ defaultDate, onCreated }: AddEventFormProps) {
  const [open, setOpen] = React.useState(false);
  const [title, setTitle] = React.useState("");
  const [date, setDate] = React.useState(defaultDate);
  const [time, setTime] = React.useState("09:00");
  const [durationMin, setDurationMin] = React.useState(60);
  const [notify, setNotify] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);
  const [warn, setWarn] = React.useState<string | null>(null);

  // Ao abrir, alinha a data com o dia selecionado no calendário.
  React.useEffect(() => {
    if (open) setDate(defaultDate);
  }, [open, defaultDate]);

  function reset() {
    setTitle("");
    setTime("09:00");
    setDurationMin(60);
    setNotify(false);
    setErrorMsg(null);
    setWarn(null);
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setErrorMsg(null);
    setWarn(null);
    if (!title.trim()) {
      setErrorMsg("Informe o título do compromisso.");
      return;
    }
    setBusy(true);
    try {
      const res = await createMyAgendaEvent({
        title: title.trim(),
        date,
        time,
        duration_min: durationMin,
        notify,
      });
      if (notify && !res.notified) {
        setWarn("Compromisso criado, mas não consegui avisar no WhatsApp.");
      }
      const dayKey = date; // dia local escolhido
      reset();
      setOpen(false);
      onCreated(dayKey);
    } catch (err) {
      setErrorMsg(
        err instanceof ApiError
          ? err.message
          : "Não consegui criar o compromisso agora.",
      );
    } finally {
      setBusy(false);
    }
  }

  if (!open) {
    return (
      <>
        {warn && (
          <Alert className="mb-3">
            <AlertDescription>{warn}</AlertDescription>
          </Alert>
        )}
        <Button onClick={() => setOpen(true)} className="gap-2">
          <CalendarPlus className="h-4 w-4" aria-hidden />
          Adicionar compromisso
        </Button>
      </>
    );
  }

  return (
    <Card className="shadow-warm">
      <CardContent className="p-5">
        <form onSubmit={handleSubmit} className="space-y-4" noValidate>
          <div className="space-y-2">
            <Label htmlFor="ev-title">Título</Label>
            <Input
              id="ev-title"
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              placeholder="Ex: Consulta com Dr. Elson"
              autoFocus
            />
          </div>

          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2">
              <Label htmlFor="ev-date">Data</Label>
              <Input
                id="ev-date"
                type="date"
                value={date}
                onChange={(e) => setDate(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="ev-time">Hora</Label>
              <Input
                id="ev-time"
                type="time"
                value={time}
                onChange={(e) => setTime(e.target.value)}
              />
            </div>
          </div>

          <div className="space-y-2">
            <Label htmlFor="ev-duration">Duração</Label>
            <select
              id="ev-duration"
              value={durationMin}
              onChange={(e) => setDurationMin(Number(e.target.value))}
              className="h-10 w-full rounded-md border border-input bg-background px-3 text-sm"
            >
              {DURATIONS.map((d) => (
                <option key={d.value} value={d.value}>
                  {d.label}
                </option>
              ))}
            </select>
          </div>

          <label className="flex items-center gap-2 text-sm text-muted-foreground">
            <input
              type="checkbox"
              checked={notify}
              onChange={(e) => setNotify(e.target.checked)}
              className="h-4 w-4 rounded border-input"
            />
            Avisar no WhatsApp
          </label>

          {errorMsg && (
            <Alert variant="destructive">
              <AlertDescription>{errorMsg}</AlertDescription>
            </Alert>
          )}

          <div className="flex gap-2">
            <Button type="submit" disabled={busy} className="gap-2">
              {busy ? (
                <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
              ) : (
                <CalendarPlus className="h-4 w-4" aria-hidden />
              )}
              Salvar compromisso
            </Button>
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                reset();
                setOpen(false);
              }}
              disabled={busy}
            >
              Cancelar
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
