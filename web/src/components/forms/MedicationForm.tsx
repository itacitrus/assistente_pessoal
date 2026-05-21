"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Plus, X } from "lucide-react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { ApiError } from "@/lib/api";
import { createDependentMedication } from "@/lib/api/family";
import { createMyMedication } from "@/lib/api/me";
import { cn } from "@/lib/utils";
import type {
  CreateMedicationBody,
  LateDosePolicy,
  MedicationDuration,
  MedicationDurationUnit,
  MedicationFrequency,
  MedicationWeekDay,
} from "@/types/api";

/**
 * Alvo do cadastro: o remédio do próprio titular (`self`) ou de um dependente.
 * Mantém o form único pras duas telas — só muda o endpoint chamado.
 */
export type MedicationTarget =
  | { kind: "self" }
  | { kind: "dependent"; dependentId: number };

export interface MedicationFormProps {
  target: MedicationTarget;
}

type Status = "idle" | "submitting" | "error";
type DurationKind = "continuous" | "period" | "until";

const MAX_TIMES = 6;

/** Dias da semana na ordem brasileira (segunda primeiro), com rotulo curto. */
const WEEK_DAYS: { value: MedicationWeekDay; label: string }[] = [
  { value: "mon", label: "Seg" },
  { value: "tue", label: "Ter" },
  { value: "wed", label: "Qua" },
  { value: "thu", label: "Qui" },
  { value: "fri", label: "Sex" },
  { value: "sat", label: "Sáb" },
  { value: "sun", label: "Dom" },
];

const DURATION_UNITS: { value: MedicationDurationUnit; label: string }[] = [
  { value: "days", label: "dias" },
  { value: "weeks", label: "semanas" },
  { value: "months", label: "meses" },
];

/**
 * Opções de orientação para dose atrasada. O Zello relata ao idoso deixando
 * claro que é recomendação do responsável, NÃO orientação médica.
 */
const LATE_DOSE_POLICIES: {
  value: LateDosePolicy;
  label: string;
  hint: string;
}[] = [
  {
    value: "consult_doctor",
    label: "Decisão do médico (padrão)",
    hint: "O Zello não orienta tomar ou pular — diz que a decisão é do médico.",
  },
  {
    value: "skip",
    label: "Pular a dose",
    hint: "Se passar do horário, orienta pular essa dose e esperar a próxima.",
  },
  {
    value: "take_keep_next",
    label: "Tomar e manter a próxima",
    hint: "Pode tomar atrasado e mantém a próxima dose no horário normal.",
  },
  {
    value: "take_recalculate",
    label: "Tomar e recalcular horários",
    hint: "Pode tomar atrasado; os próximos horários são reagendados a partir daí.",
  },
];

const DEFAULT_TOLERANCE_MIN = 30;

const TIME_RE = /^([01]\d|2[0-3]):[0-5]\d$/;

/** Data de hoje em YYYY-MM-DD (fuso local) para o mínimo do date picker. */
function todayISO(): string {
  const d = new Date();
  const tzOffset = d.getTimezoneOffset() * 60_000;
  return new Date(d.getTime() - tzOffset).toISOString().slice(0, 10);
}

export function MedicationForm({ target }: MedicationFormProps) {
  const router = useRouter();
  const [name, setName] = React.useState("");
  const [dose, setDose] = React.useState("");
  const [instructions, setInstructions] = React.useState("");
  const [times, setTimes] = React.useState<string[]>(["08:00"]);
  const [frequency, setFrequency] =
    React.useState<MedicationFrequency>("daily");
  const [days, setDays] = React.useState<MedicationWeekDay[]>([]);
  const [durationKind, setDurationKind] =
    React.useState<DurationKind>("continuous");
  const [periodCount, setPeriodCount] = React.useState("1");
  const [periodUnit, setPeriodUnit] =
    React.useState<MedicationDurationUnit>("weeks");
  const [untilDate, setUntilDate] = React.useState("");
  const [toleranceMin, setToleranceMin] = React.useState(
    String(DEFAULT_TOLERANCE_MIN),
  );
  const [latePolicy, setLatePolicy] =
    React.useState<LateDosePolicy>("consult_doctor");
  const [status, setStatus] = React.useState<Status>("idle");
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  const validTimes = times.map((t) => t.trim()).filter((t) => TIME_RE.test(t));

  const durationValid =
    durationKind === "continuous" ||
    (durationKind === "period" && Number(periodCount) >= 1) ||
    (durationKind === "until" && untilDate !== "");

  const toleranceValid =
    /^\d+$/.test(toleranceMin.trim()) &&
    Number(toleranceMin) >= 0 &&
    Number(toleranceMin) <= 720;

  const canSubmit =
    name.trim().length >= 2 &&
    dose.trim().length >= 1 &&
    validTimes.length >= 1 &&
    validTimes.length === times.length &&
    (frequency === "daily" || days.length >= 1) &&
    durationValid &&
    toleranceValid &&
    status !== "submitting";

  function updateTime(index: number, value: string) {
    setTimes((prev) => prev.map((t, i) => (i === index ? value : t)));
  }

  function addTime() {
    setTimes((prev) => (prev.length >= MAX_TIMES ? prev : [...prev, ""]));
  }

  function removeTime(index: number) {
    setTimes((prev) =>
      prev.length <= 1 ? prev : prev.filter((_, i) => i !== index),
    );
  }

  function toggleDay(value: MedicationWeekDay) {
    setDays((prev) =>
      prev.includes(value)
        ? prev.filter((d) => d !== value)
        : [...prev, value],
    );
  }

  function buildDuration(): MedicationDuration | undefined {
    if (durationKind === "continuous") return undefined;
    if (durationKind === "period") {
      return { kind: "period", count: Number(periodCount), unit: periodUnit };
    }
    return { kind: "until", until: untilDate };
  }

  function resetForm() {
    setName("");
    setDose("");
    setInstructions("");
    setTimes(["08:00"]);
    setFrequency("daily");
    setDays([]);
    setDurationKind("continuous");
    setPeriodCount("1");
    setPeriodUnit("weeks");
    setUntilDate("");
    setToleranceMin(String(DEFAULT_TOLERANCE_MIN));
    setLatePolicy("consult_doctor");
    setStatus("idle");
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setStatus("submitting");
    setErrorMsg(null);

    const tolerance = Number(toleranceMin);
    const body: CreateMedicationBody = {
      name: name.trim(),
      dose: dose.trim(),
      instructions: instructions.trim(),
      times: validTimes,
      frequency,
      ...(frequency === "weekly" ? { days } : {}),
      ...(buildDuration() ? { duration: buildDuration() } : {}),
      ...(Number.isFinite(tolerance) ? { tolerance_minutes: tolerance } : {}),
      late_dose_policy: latePolicy,
    };

    try {
      if (target.kind === "self") {
        await createMyMedication(body);
      } else {
        await createDependentMedication(target.dependentId, body);
      }
      resetForm();
      router.refresh();
    } catch (err) {
      setStatus("error");
      setErrorMsg(
        err instanceof ApiError
          ? err.message
          : "Não consegui cadastrar agora. Tente novamente em alguns segundos.",
      );
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-6" noValidate>
      <div className="space-y-2">
        <Label htmlFor="med-name">Nome do remédio</Label>
        <Input
          id="med-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Losartana"
          required
        />
      </div>

      <div className="space-y-2">
        <Label htmlFor="med-dose">Dose</Label>
        <Input
          id="med-dose"
          value={dose}
          onChange={(e) => setDose(e.target.value)}
          placeholder="50 mg, 1 comprimido"
          required
        />
      </div>

      <div className="space-y-2">
        <Label htmlFor="med-instructions">Instruções (opcional)</Label>
        <Input
          id="med-instructions"
          value={instructions}
          onChange={(e) => setInstructions(e.target.value)}
          placeholder="Tomar após o café da manhã"
        />
      </div>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium leading-none">Horários</legend>
        <p className="text-sm text-muted-foreground">
          De 1 a {MAX_TIMES} horários por dia. O Zello lembra na hora certa.
        </p>
        <div className="space-y-2">
          {times.map((t, i) => (
            <div key={i} className="flex items-center gap-2">
              <Input
                type="time"
                aria-label={`Horário ${i + 1}`}
                value={t}
                onChange={(e) => updateTime(i, e.target.value)}
                className="max-w-[10rem]"
                required
              />
              {times.length > 1 && (
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  onClick={() => removeTime(i)}
                  aria-label={`Remover horário ${i + 1}`}
                >
                  <X className="h-4 w-4" aria-hidden />
                </Button>
              )}
            </div>
          ))}
        </div>
        {times.length < MAX_TIMES && (
          <Button type="button" variant="outline" size="sm" onClick={addTime}>
            <Plus className="h-4 w-4" aria-hidden />
            Adicionar horário
          </Button>
        )}
      </fieldset>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium leading-none">Frequência</legend>
        <RadioGroup
          value={frequency}
          onValueChange={(v) => setFrequency(v as MedicationFrequency)}
          className="gap-3"
        >
          <label
            htmlFor="freq-daily"
            className="flex cursor-pointer items-center gap-3 rounded-md border p-3"
          >
            <RadioGroupItem value="daily" id="freq-daily" />
            <span className="text-base">Todo dia</span>
          </label>
          <label
            htmlFor="freq-weekly"
            className="flex cursor-pointer items-center gap-3 rounded-md border p-3"
          >
            <RadioGroupItem value="weekly" id="freq-weekly" />
            <span className="text-base">Dias específicos</span>
          </label>
        </RadioGroup>

        {frequency === "weekly" && (
          <div className="space-y-2 rounded-md border bg-muted/30 p-3">
            <p className="text-sm text-muted-foreground">
              Escolha os dias da semana:
            </p>
            <div className="flex flex-wrap gap-2">
              {WEEK_DAYS.map((d) => {
                const selected = days.includes(d.value);
                return (
                  <button
                    key={d.value}
                    type="button"
                    aria-pressed={selected}
                    onClick={() => toggleDay(d.value)}
                    className={cn(
                      "min-w-[3rem] rounded-full border px-3 py-1.5 text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
                      selected
                        ? "border-[--zello-emerald] bg-[--zello-emerald] text-primary-foreground"
                        : "border-input bg-background hover:bg-accent hover:text-accent-foreground",
                    )}
                  >
                    {d.label}
                  </button>
                );
              })}
            </div>
            {days.length === 0 && (
              <p className="text-sm text-muted-foreground">
                Selecione ao menos um dia.
              </p>
            )}
          </div>
        )}
      </fieldset>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium leading-none">
          Por quanto tempo?
        </legend>
        <RadioGroup
          value={durationKind}
          onValueChange={(v) => setDurationKind(v as DurationKind)}
          className="gap-3"
        >
          <label
            htmlFor="dur-cont"
            className="flex cursor-pointer items-center gap-3 rounded-md border p-3"
          >
            <RadioGroupItem value="continuous" id="dur-cont" />
            <span className="text-base">Contínuo (sem data de término)</span>
          </label>
          <label
            htmlFor="dur-period"
            className="flex cursor-pointer items-center gap-3 rounded-md border p-3"
          >
            <RadioGroupItem value="period" id="dur-period" />
            <span className="text-base">Por um período</span>
          </label>
          <label
            htmlFor="dur-until"
            className="flex cursor-pointer items-center gap-3 rounded-md border p-3"
          >
            <RadioGroupItem value="until" id="dur-until" />
            <span className="text-base">Até uma data específica</span>
          </label>
        </RadioGroup>

        {durationKind === "period" && (
          <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 p-3">
            <span className="text-sm text-muted-foreground">Por</span>
            <Input
              type="number"
              min={1}
              inputMode="numeric"
              aria-label="Quantidade"
              value={periodCount}
              onChange={(e) => setPeriodCount(e.target.value)}
              className="max-w-[5rem]"
            />
            <select
              aria-label="Unidade"
              value={periodUnit}
              onChange={(e) =>
                setPeriodUnit(e.target.value as MedicationDurationUnit)
              }
              className="h-10 rounded-md border border-input bg-background px-3 text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
            >
              {DURATION_UNITS.map((u) => (
                <option key={u.value} value={u.value}>
                  {u.label}
                </option>
              ))}
            </select>
          </div>
        )}

        {durationKind === "until" && (
          <div className="space-y-2 rounded-md border bg-muted/30 p-3">
            <Label htmlFor="dur-until-date">Data de término</Label>
            <Input
              id="dur-until-date"
              type="date"
              min={todayISO()}
              value={untilDate}
              onChange={(e) => setUntilDate(e.target.value)}
              className="max-w-[12rem]"
            />
          </div>
        )}
      </fieldset>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium leading-none">
          Tolerância de atraso
        </legend>
        <p className="text-sm text-muted-foreground">
          Quanto tempo depois do horário o Zello espera antes de avisar você
          (em segredo, sem pressionar o titular).
        </p>
        <div className="flex items-center gap-2">
          <Input
            type="number"
            min={0}
            max={720}
            inputMode="numeric"
            aria-label="Minutos de tolerância"
            value={toleranceMin}
            onChange={(e) => setToleranceMin(e.target.value)}
            className="max-w-[6rem]"
          />
          <span className="text-sm text-muted-foreground">minutos</span>
        </div>
        {!toleranceValid && (
          <p className="text-sm text-destructive">Use de 0 a 720 minutos.</p>
        )}
      </fieldset>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium leading-none">
          Se passar do horário
        </legend>
        <p className="text-sm text-muted-foreground">
          Sua orientação. O Zello a repassa ao titular deixando claro que é
          recomendação sua, não orientação médica.
        </p>
        <RadioGroup
          value={latePolicy}
          onValueChange={(v) => setLatePolicy(v as LateDosePolicy)}
          className="gap-3"
        >
          {LATE_DOSE_POLICIES.map((p) => (
            <label
              key={p.value}
              htmlFor={`late-${p.value}`}
              className="flex cursor-pointer items-start gap-3 rounded-md border p-3"
            >
              <RadioGroupItem
                value={p.value}
                id={`late-${p.value}`}
                className="mt-1"
              />
              <span className="space-y-0.5">
                <span className="block text-base">{p.label}</span>
                <span className="block text-sm text-muted-foreground">
                  {p.hint}
                </span>
              </span>
            </label>
          ))}
        </RadioGroup>
      </fieldset>

      {status === "error" && errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}

      <Button type="submit" disabled={!canSubmit} className="w-full">
        {status === "submitting" ? "Cadastrando..." : "Cadastrar remédio"}
      </Button>
    </form>
  );
}
