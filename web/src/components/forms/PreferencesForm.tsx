"use client";

import * as React from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ApiError } from "@/lib/api";
import { updateMe } from "@/lib/api/users";
import type {
  AutoConfirmTimeout,
  ReminderBefore,
  UpdateMeBody,
  User,
  WeekDay,
} from "@/types/api";

export interface PreferencesFormProps {
  user: User;
}

type Status = "idle" | "saving" | "saved" | "error";

const REMINDER_OPTIONS: { id: ReminderBefore; label: string }[] = [
  { id: "15m", label: "15 minutos" },
  { id: "30m", label: "30 minutos" },
  { id: "1h", label: "1 hora" },
  { id: "2h", label: "2 horas" },
  { id: "4h", label: "4 horas" },
];

const AUTO_CONFIRM_OPTIONS: { id: AutoConfirmTimeout; label: string }[] = [
  { id: "30m", label: "30 minutos" },
  { id: "1h", label: "1 hora" },
  { id: "2h", label: "2 horas" },
  { id: "4h", label: "4 horas" },
  { id: "never", label: "Nunca" },
];

const WEEKDAY_OPTIONS: { id: WeekDay; label: string }[] = [
  { id: "sunday", label: "Domingo" },
  { id: "monday", label: "Segunda" },
  { id: "tuesday", label: "Terca" },
  { id: "wednesday", label: "Quarta" },
  { id: "thursday", label: "Quinta" },
  { id: "friday", label: "Sexta" },
  { id: "saturday", label: "Sabado" },
];

function isValidHHMM(input: string): boolean {
  return /^[0-2]\d:[0-5]\d$/.test(input);
}

export function PreferencesForm({ user }: PreferencesFormProps) {
  const [name, setName] = React.useState(user.name);
  const [dailySummary, setDailySummary] = React.useState(
    user.daily_summary_time,
  );
  const [weeklyDay, setWeeklyDay] = React.useState<WeekDay>(
    user.weekly_summary_day,
  );
  const [weeklyTime, setWeeklyTime] = React.useState(user.weekly_summary_time);
  const [reminder, setReminder] = React.useState<ReminderBefore>(
    user.reminder_before,
  );
  const [autoConfirm, setAutoConfirm] = React.useState<AutoConfirmTimeout>(
    user.auto_confirm_timeout,
  );
  const [inactivityHours, setInactivityHours] = React.useState<number>(
    user.inactivity_threshold_hours,
  );

  const [status, setStatus] = React.useState<Status>("idle");
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  const validTimes = isValidHHMM(dailySummary) && isValidHHMM(weeklyTime);
  const validInactivity = inactivityHours >= 4 && inactivityHours <= 168;
  const canSubmit =
    name.trim().length >= 2 &&
    validTimes &&
    validInactivity &&
    status !== "saving";

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setStatus("saving");
    setErrorMsg(null);
    const body: UpdateMeBody = {
      name: name.trim(),
      daily_summary_time: dailySummary,
      weekly_summary_day: weeklyDay,
      weekly_summary_time: weeklyTime,
      reminder_before: reminder,
      auto_confirm_timeout: autoConfirm,
      inactivity_threshold_hours: inactivityHours,
    };
    try {
      await updateMe(body);
      setStatus("saved");
    } catch (err) {
      setStatus("error");
      if (err instanceof ApiError) {
        setErrorMsg(err.message);
      } else {
        setErrorMsg("Nao consegui salvar agora. Tente novamente.");
      }
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-6" noValidate>
      <div className="space-y-2">
        <Label htmlFor="pref-name">Nome</Label>
        <Input
          id="pref-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          required
        />
      </div>

      <div className="space-y-2">
        <Label htmlFor="pref-daily">Hora do resumo diario</Label>
        <Input
          id="pref-daily"
          type="time"
          value={dailySummary}
          onChange={(e) => setDailySummary(e.target.value)}
          required
        />
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="pref-weekly-day">Dia do resumo semanal</Label>
          <Select
            value={weeklyDay}
            onValueChange={(v) => setWeeklyDay(v as WeekDay)}
          >
            <SelectTrigger id="pref-weekly-day">
              <SelectValue placeholder="Dia da semana" />
            </SelectTrigger>
            <SelectContent>
              {WEEKDAY_OPTIONS.map((d) => (
                <SelectItem key={d.id} value={d.id}>
                  {d.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="space-y-2">
          <Label htmlFor="pref-weekly-time">Hora do resumo semanal</Label>
          <Input
            id="pref-weekly-time"
            type="time"
            value={weeklyTime}
            onChange={(e) => setWeeklyTime(e.target.value)}
            required
          />
        </div>
      </div>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium">
          Lembrete antes do evento
        </legend>
        <RadioGroup
          value={reminder}
          onValueChange={(v) => setReminder(v as ReminderBefore)}
          className="grid grid-cols-2 gap-2 sm:grid-cols-5"
        >
          {REMINDER_OPTIONS.map((opt) => (
            <div key={opt.id} className="flex items-center gap-2">
              <RadioGroupItem id={`rem-${opt.id}`} value={opt.id} />
              <Label htmlFor={`rem-${opt.id}`} className="font-normal">
                {opt.label}
              </Label>
            </div>
          ))}
        </RadioGroup>
      </fieldset>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium">
          Tempo para auto-confirmar
        </legend>
        <RadioGroup
          value={autoConfirm}
          onValueChange={(v) => setAutoConfirm(v as AutoConfirmTimeout)}
          className="grid grid-cols-2 gap-2 sm:grid-cols-5"
        >
          {AUTO_CONFIRM_OPTIONS.map((opt) => (
            <div key={opt.id} className="flex items-center gap-2">
              <RadioGroupItem id={`ac-${opt.id}`} value={opt.id} />
              <Label htmlFor={`ac-${opt.id}`} className="font-normal">
                {opt.label}
              </Label>
            </div>
          ))}
        </RadioGroup>
      </fieldset>

      <div className="space-y-2">
        <Label htmlFor="pref-inactivity">
          Considerar inativo apos quantas horas sem resposta?
        </Label>
        <Input
          id="pref-inactivity"
          type="number"
          min={4}
          max={168}
          step={1}
          value={inactivityHours}
          onChange={(e) =>
            setInactivityHours(Number.parseInt(e.target.value, 10) || 0)
          }
          required
        />
        <p className="text-xs text-muted-foreground">
          Entre 4 e 168 horas (1 semana).
        </p>
      </div>

      {status === "error" && errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}
      {status === "saved" && (
        <Alert variant="success">
          <AlertDescription>Preferencias salvas.</AlertDescription>
        </Alert>
      )}

      <Button type="submit" disabled={!canSubmit}>
        {status === "saving" ? "Salvando..." : "Salvar"}
      </Button>
    </form>
  );
}
