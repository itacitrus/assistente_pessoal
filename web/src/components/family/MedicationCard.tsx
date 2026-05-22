"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Clock, History, Pencil, Pill, Trash2 } from "lucide-react";

import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ApiError } from "@/lib/api";
import { deleteDependentMedication } from "@/lib/api/family";
import { deleteMyMedication } from "@/lib/api/me";
import {
  MedicationForm,
  type MedicationTarget,
} from "@/components/forms/MedicationForm";
import { MedicationIntakeHistory } from "@/components/family/MedicationIntakeHistory";
import type { LateDosePolicy, MedicationItem } from "@/types/api";

export interface MedicationCardProps {
  target: MedicationTarget;
  medication: MedicationItem;
}

/** Resumo curto da política de dose atrasada para o responsável conferir. */
const LATE_POLICY_LABEL: Record<LateDosePolicy, string> = {
  consult_doctor: "Atraso: decisão do médico",
  skip: "Atraso: pular a dose",
  take_keep_next: "Atraso: tomar e manter a próxima",
  take_recalculate: "Atraso: tomar e recalcular horários",
};

export function MedicationCard({ target, medication }: MedicationCardProps) {
  const router = useRouter();
  const [removing, setRemoving] = React.useState(false);
  const [editing, setEditing] = React.useState(false);
  const [historyOpen, setHistoryOpen] = React.useState(false);
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  async function handleRemove() {
    const ok = window.confirm(
      `Remover "${medication.name}"? O Zello vai parar de lembrar dos horários desse remédio.`,
    );
    if (!ok) return;
    setRemoving(true);
    setErrorMsg(null);
    try {
      if (target.kind === "self") {
        await deleteMyMedication(medication.id);
      } else {
        await deleteDependentMedication(target.dependentId, medication.id);
      }
      router.refresh();
    } catch (err) {
      setRemoving(false);
      if (err instanceof ApiError) {
        setErrorMsg(err.message);
      } else {
        setErrorMsg("Não consegui remover agora. Tente novamente.");
      }
    }
  }

  if (editing) {
    return (
      <Card className="shadow-warm">
        <CardContent className="space-y-4 p-5">
          <div className="flex items-center justify-between">
            <p className="font-medium text-foreground">
              Editar {medication.name}
            </p>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setEditing(false)}
            >
              Cancelar
            </Button>
          </div>
          <MedicationForm
            target={target}
            medication={medication}
            onDone={() => setEditing(false)}
          />
        </CardContent>
      </Card>
    );
  }

  return (
    <Card className="shadow-warm">
      <CardContent className="flex flex-col gap-3 p-5 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex min-w-0 gap-3">
          <div className="mt-0.5 flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-[--zello-emerald]/10 text-[--zello-emerald]">
            <Pill className="h-5 w-5" aria-hidden />
          </div>
          <div className="min-w-0 space-y-1">
            <div className="flex flex-wrap items-baseline gap-x-2">
              <p className="font-medium text-foreground">{medication.name}</p>
              {medication.dose ? (
                <span className="text-sm text-muted-foreground">
                  {medication.dose}
                </span>
              ) : null}
              {!medication.active ? (
                <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
                  pausado
                </span>
              ) : medication.ends_at ? (
                <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs font-medium text-amber-800">
                  temporário
                </span>
              ) : null}
              {medication.active && !medication.require_confirmation ? (
                <span className="rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
                  só lembrete
                </span>
              ) : null}
            </div>
            {medication.schedule ? (
              <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
                <Clock className="h-3.5 w-3.5 shrink-0" aria-hidden />
                <span>{medication.schedule}</span>
              </p>
            ) : null}
            {medication.instructions ? (
              <p className="text-sm text-muted-foreground">
                {medication.instructions}
              </p>
            ) : null}
            <p className="text-xs text-muted-foreground">
              Tolerância: {medication.tolerance_minutes} min ·{" "}
              {LATE_POLICY_LABEL[medication.late_dose_policy] ??
                LATE_POLICY_LABEL.consult_doctor}
            </p>
            {errorMsg ? (
              <Alert variant="destructive" className="mt-2">
                <AlertDescription>{errorMsg}</AlertDescription>
              </Alert>
            ) : null}
          </div>
        </div>
        <div className="flex shrink-0 flex-wrap gap-1 self-start">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => {
              setErrorMsg(null);
              setEditing(true);
            }}
            disabled={removing}
            className="text-muted-foreground hover:text-foreground"
          >
            <Pencil className="h-4 w-4" aria-hidden />
            Editar
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => setHistoryOpen((v) => !v)}
            disabled={removing}
            aria-expanded={historyOpen}
            className="text-muted-foreground hover:text-foreground"
          >
            <History className="h-4 w-4" aria-hidden />
            {historyOpen ? "Ocultar histórico" : "Ver histórico"}
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={handleRemove}
            disabled={removing}
            className="text-muted-foreground hover:text-destructive"
          >
            <Trash2 className="h-4 w-4" aria-hidden />
            {removing ? "Removendo..." : "Remover"}
          </Button>
        </div>
      </CardContent>
      {historyOpen ? (
        <CardContent className="border-t p-5">
          <p className="mb-3 text-sm font-medium text-foreground">
            Histórico de toma (últimos 30 dias)
          </p>
          <MedicationIntakeHistory
            target={target}
            medicationId={medication.id}
          />
        </CardContent>
      ) : null}
    </Card>
  );
}
