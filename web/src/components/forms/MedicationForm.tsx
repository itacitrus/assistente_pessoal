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
import { cn } from "@/lib/utils";
import type {
  CreateMedicationBody,
  MedicationFrequency,
  MedicationWeekDay,
} from "@/types/api";

export interface MedicationFormProps {
  /** Id do dependente dono do remedio. */
  dependentId: number;
}

type Status = "idle" | "submitting" | "error";

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

const TIME_RE = /^([01]\d|2[0-3]):[0-5]\d$/;

export function MedicationForm({ dependentId }: MedicationFormProps) {
  const router = useRouter();
  const [name, setName] = React.useState("");
  const [dose, setDose] = React.useState("");
  const [instructions, setInstructions] = React.useState("");
  const [times, setTimes] = React.useState<string[]>(["08:00"]);
  const [frequency, setFrequency] =
    React.useState<MedicationFrequency>("daily");
  const [days, setDays] = React.useState<MedicationWeekDay[]>([]);
  const [status, setStatus] = React.useState<Status>("idle");
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  const validTimes = times
    .map((t) => t.trim())
    .filter((t) => TIME_RE.test(t));

  const canSubmit =
    name.trim().length >= 2 &&
    dose.trim().length >= 1 &&
    validTimes.length >= 1 &&
    validTimes.length === times.length &&
    (frequency === "daily" || days.length >= 1) &&
    status !== "submitting";

  function updateTime(index: number, value: string) {
    setTimes((prev) => prev.map((t, i) => (i === index ? value : t)));
  }

  function addTime() {
    setTimes((prev) =>
      prev.length >= MAX_TIMES ? prev : [...prev, ""],
    );
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

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setStatus("submitting");
    setErrorMsg(null);

    const body: CreateMedicationBody = {
      name: name.trim(),
      dose: dose.trim(),
      instructions: instructions.trim(),
      times: validTimes,
      frequency,
      ...(frequency === "weekly" ? { days } : {}),
    };

    try {
      await createDependentMedication(dependentId, body);
      // Limpa o formulario e recarrega a lista (server component pai).
      setName("");
      setDose("");
      setInstructions("");
      setTimes(["08:00"]);
      setFrequency("daily");
      setDays([]);
      setStatus("idle");
      router.refresh();
    } catch (err) {
      setStatus("error");
      if (err instanceof ApiError) {
        setErrorMsg(err.message);
      } else {
        setErrorMsg(
          "Não consegui cadastrar agora. Tente novamente em alguns segundos.",
        );
      }
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
        <legend className="text-sm font-medium leading-none">
          Horários
        </legend>
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
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={addTime}
          >
            <Plus className="h-4 w-4" aria-hidden />
            Adicionar horário
          </Button>
        )}
      </fieldset>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium leading-none">
          Frequência
        </legend>
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
