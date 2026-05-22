"use client";

import * as React from "react";

import { IntakeHistoryList } from "@/components/family/IntakeHistoryList";
import { Skeleton } from "@/components/ui/skeleton";
import type { MedicationTarget } from "@/components/forms/MedicationForm";
import { getDependentIntakes } from "@/lib/api/family";
import { getMyIntakes } from "@/lib/api/me";
import type { IntakeEntry } from "@/types/api";

export interface MedicationIntakeHistoryProps {
  target: MedicationTarget;
  medicationId: number;
  /** Janela em dias (default 30). Teto do backend é 90. */
  days?: number;
}

/**
 * Histórico de tomadas de UM remédio, carregado sob demanda (quando o card
 * expande). Filtra pelo `medication_id` no backend; some o nome do remédio na
 * lista (já está no contexto do card).
 */
export function MedicationIntakeHistory({
  target,
  medicationId,
  days = 30,
}: MedicationIntakeHistoryProps) {
  const [state, setState] = React.useState<"loading" | "ready" | "error">(
    "loading",
  );
  const [intakes, setIntakes] = React.useState<IntakeEntry[]>([]);

  React.useEffect(() => {
    let active = true;
    setState("loading");
    (async () => {
      try {
        const res =
          target.kind === "self"
            ? await getMyIntakes({ days, medicationId })
            : await getDependentIntakes(target.dependentId, {
                days,
                medicationId,
              });
        if (active) {
          setIntakes(res.intakes);
          setState("ready");
        }
      } catch {
        if (active) setState("error");
      }
    })();
    return () => {
      active = false;
    };
  }, [target, medicationId, days]);

  if (state === "loading") {
    return (
      <div className="space-y-2">
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-9 w-full" />
        <Skeleton className="h-9 w-full" />
      </div>
    );
  }
  if (state === "error") {
    return (
      <p className="text-sm text-muted-foreground">
        Não consegui carregar o histórico agora. Tente novamente em instantes.
      </p>
    );
  }
  return (
    <IntakeHistoryList
      intakes={intakes}
      showMedicationName={false}
      emptyText={`Nenhuma dose registrada nos últimos ${days} dias.`}
    />
  );
}
