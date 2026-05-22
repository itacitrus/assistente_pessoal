import type { IntakeEntry, IntakeStatus } from "@/types/api";
import { cn } from "@/lib/utils";

const TZ = "America/Sao_Paulo";

/** Rótulo + cores por status. Para o responsável, "missed" e "escalated" são
 * ambos simplesmente "Não tomada" — a escalação à família é detalhe interno. */
const STATUS_META: Record<
  IntakeStatus,
  { label: string; dot: string; text: string }
> = {
  taken: { label: "Tomada", dot: "bg-emerald-500", text: "text-emerald-700" },
  missed: { label: "Não tomada", dot: "bg-red-500", text: "text-red-700" },
  escalated: { label: "Não tomada", dot: "bg-red-500", text: "text-red-700" },
  skipped: { label: "Pulada", dot: "bg-amber-500", text: "text-amber-700" },
  pending: {
    label: "Aguardando",
    dot: "bg-muted-foreground/40",
    text: "text-muted-foreground",
  },
  // Remédio "só lembrete": não exige confirmação, então não sabemos se tomou.
  unknown: {
    label: "Sem confirmação exigida",
    dot: "bg-muted-foreground/30",
    text: "text-muted-foreground",
  },
};

/** Chave estável (YYYY-MM-DD em BRT) para agrupar por dia. */
function dayKey(iso: string): string {
  return new Intl.DateTimeFormat("en-CA", {
    timeZone: TZ,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(new Date(iso));
}

function dayLabel(iso: string): string {
  return new Intl.DateTimeFormat("pt-BR", {
    timeZone: TZ,
    weekday: "short",
    day: "2-digit",
    month: "2-digit",
  }).format(new Date(iso));
}

function timeLabel(iso: string): string {
  return new Intl.DateTimeFormat("pt-BR", {
    timeZone: TZ,
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(iso));
}

export interface IntakeHistoryListProps {
  intakes: IntakeEntry[];
  /** Mostra nome+dose do remédio em cada linha. Off na visão de um só remédio. */
  showMedicationName?: boolean;
  emptyText?: string;
}

/**
 * Lista de ocorrências de dose agrupadas por dia (fuso de Brasília), com um
 * marcador colorido por status. Componente puro — serve tanto no server
 * (detalhe de aderência) quanto no client (histórico por remédio).
 *
 * O backend já entrega `intakes` em ordem decrescente de horário, então
 * agrupar na ordem de chegada mantém o dia mais recente no topo.
 */
export function IntakeHistoryList({
  intakes,
  showMedicationName = true,
  emptyText = "Nenhuma dose registrada nesta janela.",
}: IntakeHistoryListProps) {
  if (intakes.length === 0) {
    return <p className="text-sm text-muted-foreground">{emptyText}</p>;
  }

  const groups = new Map<string, { label: string; items: IntakeEntry[] }>();
  for (const it of intakes) {
    const key = dayKey(it.scheduled_at);
    const g = groups.get(key);
    if (g) {
      g.items.push(it);
    } else {
      groups.set(key, { label: dayLabel(it.scheduled_at), items: [it] });
    }
  }

  return (
    <div className="space-y-5">
      {Array.from(groups.entries()).map(([key, g]) => (
        <div key={key} className="space-y-2">
          <h3 className="text-sm font-medium capitalize text-muted-foreground">
            {g.label}
          </h3>
          <ul className="space-y-1.5">
            {g.items.map((it, i) => {
              const meta = STATUS_META[it.status] ?? STATUS_META.pending;
              return (
                <li
                  key={`${key}-${i}`}
                  className="flex items-center gap-3 rounded-md border bg-card px-3 py-2 text-sm"
                >
                  <span
                    className={cn("h-2 w-2 shrink-0 rounded-full", meta.dot)}
                    aria-hidden
                  />
                  <span className="w-12 shrink-0 tabular-nums text-muted-foreground">
                    {timeLabel(it.scheduled_at)}
                  </span>
                  {showMedicationName ? (
                    <span className="min-w-0 flex-1 truncate text-foreground">
                      {it.medication_name}
                      {it.dose ? (
                        <span className="text-muted-foreground">
                          {" "}
                          · {it.dose}
                        </span>
                      ) : null}
                    </span>
                  ) : null}
                  <span
                    className={cn(
                      "ml-auto shrink-0 font-medium",
                      meta.text,
                    )}
                  >
                    {meta.label}
                  </span>
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </div>
  );
}
