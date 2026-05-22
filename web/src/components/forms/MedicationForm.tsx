"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Plus, X } from "lucide-react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Switch } from "@/components/ui/switch";
import { ApiError } from "@/lib/api";
import {
  createDependentMedication,
  updateDependentMedication,
} from "@/lib/api/family";
import { createMyMedication, searchDrugs, updateMyMedication } from "@/lib/api/me";
import { cn } from "@/lib/utils";
import type {
  CreateMedicationBody,
  DrugMatch,
  LateDosePolicy,
  MedicationDuration,
  MedicationDurationUnit,
  MedicationFrequency,
  MedicationItem,
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
  /** Quando presente, o form entra em modo edição (PATCH) desse medicamento. */
  medication?: MedicationItem;
  /** Chamado após salvar com sucesso (ex: fechar o modal de edição). */
  onDone?: () => void;
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

export function MedicationForm({
  target,
  medication,
  onDone,
}: MedicationFormProps) {
  const router = useRouter();
  const isEdit = medication != null;
  const [name, setName] = React.useState(medication?.name ?? "");
  const [dose, setDose] = React.useState(medication?.dose ?? "");
  const [instructions, setInstructions] = React.useState(
    medication?.instructions ?? "",
  );
  const [times, setTimes] = React.useState<string[]>(
    medication?.times?.length ? medication.times : ["08:00"],
  );
  const [frequency, setFrequency] = React.useState<MedicationFrequency>(
    medication?.frequency ?? "daily",
  );
  const [days, setDays] = React.useState<MedicationWeekDay[]>(
    medication?.days ?? [],
  );
  const [durationKind, setDurationKind] = React.useState<DurationKind>(
    medication?.ends_at ? "until" : "continuous",
  );
  const [periodCount, setPeriodCount] = React.useState("1");
  const [periodUnit, setPeriodUnit] =
    React.useState<MedicationDurationUnit>("weeks");
  const [untilDate, setUntilDate] = React.useState(medication?.ends_at ?? "");
  const [toleranceMin, setToleranceMin] = React.useState(
    String(medication?.tolerance_minutes ?? DEFAULT_TOLERANCE_MIN),
  );
  const [latePolicy, setLatePolicy] = React.useState<LateDosePolicy>(
    medication?.late_dose_policy ?? "consult_doctor",
  );
  // Default true (exigir confirmação) quando o campo não veio (cadastro novo).
  const [requireConfirmation, setRequireConfirmation] = React.useState(
    medication?.require_confirmation ?? true,
  );
  const [status, setStatus] = React.useState<Status>("idle");
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  // Autocomplete do catálogo ANVISA/CMED. catalogId vincula o cadastro à
  // apresentação escolhida; some quando o usuário edita o nome à mão.
  const [catalogId, setCatalogId] = React.useState<number | undefined>(
    undefined,
  );
  const [suggestions, setSuggestions] = React.useState<DrugMatch[]>([]);
  const [showSuggest, setShowSuggest] = React.useState(false);
  // Pula a busca quando o nome muda por seleção (não por digitação) — e na
  // montagem (modo edição já vem com nome preenchido).
  const suppressSearchRef = React.useRef(true);

  React.useEffect(() => {
    if (suppressSearchRef.current) {
      suppressSearchRef.current = false;
      return;
    }
    const q = name.trim();
    if (q.length < 2) {
      setSuggestions([]);
      return;
    }
    const ctrl = new AbortController();
    const timer = setTimeout(() => {
      searchDrugs(q, 8, ctrl.signal)
        .then((res) => {
          setSuggestions(res.matches);
          setShowSuggest(true);
        })
        .catch(() => {
          // Autocomplete é best-effort: aborts e falhas de rede não atrapalham
          // o cadastro manual.
        });
    }, 250);
    return () => {
      clearTimeout(timer);
      ctrl.abort();
    };
  }, [name]);

  function selectSuggestion(m: DrugMatch) {
    suppressSearchRef.current = true;
    setName(m.commercial_name);
    if (m.concentration.trim()) setDose(m.concentration.trim());
    setCatalogId(m.id);
    setSuggestions([]);
    setShowSuggest(false);
  }

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
    setRequireConfirmation(true);
    setCatalogId(undefined);
    setSuggestions([]);
    setShowSuggest(false);
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
      ...(catalogId ? { catalog_id: catalogId } : {}),
      late_dose_policy: latePolicy,
      require_confirmation: requireConfirmation,
    };

    try {
      if (isEdit) {
        if (target.kind === "self") {
          await updateMyMedication(medication.id, body);
        } else {
          await updateDependentMedication(target.dependentId, medication.id, body);
        }
      } else if (target.kind === "self") {
        await createMyMedication(body);
      } else {
        await createDependentMedication(target.dependentId, body);
      }
      if (!isEdit) resetForm();
      router.refresh();
      onDone?.();
    } catch (err) {
      setStatus("error");
      setErrorMsg(
        err instanceof ApiError
          ? err.message
          : isEdit
            ? "Não consegui salvar as alterações agora. Tente novamente em alguns segundos."
            : "Não consegui cadastrar agora. Tente novamente em alguns segundos.",
      );
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-6" noValidate>
      <div className="space-y-2">
        <Label htmlFor="med-name">Nome do remédio</Label>
        <div className="relative">
          <Input
            id="med-name"
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              setCatalogId(undefined);
            }}
            onFocus={() => {
              if (suggestions.length > 0) setShowSuggest(true);
            }}
            onBlur={() => {
              // Atrasa pra o clique numa sugestão registrar antes de fechar.
              window.setTimeout(() => setShowSuggest(false), 150);
            }}
            placeholder="Losartana"
            autoComplete="off"
            role="combobox"
            aria-expanded={showSuggest && suggestions.length > 0}
            aria-autocomplete="list"
            required
          />
          {showSuggest && suggestions.length > 0 && (
            <ul
              className="absolute z-20 mt-1 max-h-72 w-full overflow-auto rounded-md border bg-background py-1 shadow-md"
              role="listbox"
            >
              {suggestions.map((m) => (
                <li key={m.id} role="option" aria-selected={catalogId === m.id}>
                  <button
                    type="button"
                    // onMouseDown (não onClick) pra disparar antes do onBlur do input.
                    onMouseDown={(e) => {
                      e.preventDefault();
                      selectSuggestion(m);
                    }}
                    className="flex w-full flex-col items-start gap-0.5 px-3 py-2 text-left hover:bg-muted focus:bg-muted focus:outline-none"
                  >
                    <span className="text-sm font-medium">
                      {m.commercial_name}
                      {m.concentration ? (
                        <span className="text-muted-foreground">
                          {" "}
                          · {m.concentration}
                        </span>
                      ) : null}
                    </span>
                    {m.active_ingredient ? (
                      <span className="text-xs capitalize text-muted-foreground">
                        {m.active_ingredient.toLowerCase()}
                      </span>
                    ) : null}
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
        <p className="text-xs text-muted-foreground">
          Comece a digitar — sugerimos nomes e doses do catálogo da ANVISA.
        </p>
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
          Exigir confirmação de toma
        </legend>
        <label className="flex items-start justify-between gap-4 rounded-lg border p-3">
          <span className="text-sm text-muted-foreground">
            {requireConfirmation
              ? "O Zello pede confirmação e, se não vier, lembra de novo e avisa você. Recomendado para quem precisa de acompanhamento."
              : "O Zello só lembra na hora, sem cobrar resposta. Se não confirmar, a dose fica como “não sei” (nem tomada, nem perdida). Bom para quem é independente e só quer o lembrete."}
          </span>
          <Switch
            checked={requireConfirmation}
            onCheckedChange={setRequireConfirmation}
            aria-label="Exigir confirmação de toma"
          />
        </label>
      </fieldset>

      <fieldset className="space-y-3">
        <legend className="text-sm font-medium leading-none">
          Tolerância de atraso
        </legend>
        <p className="text-sm text-muted-foreground">
          {requireConfirmation
            ? "Quanto tempo depois do horário o Zello espera antes de avisar você (em segredo, sem pressionar o titular)."
            : "Quanto tempo depois do horário o Zello espera antes de marcar a dose como “não sei” (sem aviso a ninguém)."}
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
        {status === "submitting"
          ? isEdit
            ? "Salvando..."
            : "Cadastrando..."
          : isEdit
            ? "Salvar alterações"
            : "Cadastrar remédio"}
      </Button>
    </form>
  );
}
