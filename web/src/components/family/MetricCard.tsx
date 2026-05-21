import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import type { MedicationStats } from "@/types/api";
import { cn } from "@/lib/utils";

export interface MetricCardProps {
  /** Estatisticas de medicacao no periodo da consulta (default 14 dias). */
  data: MedicationStats;
  /** Janela em dias (default 14). Usada para o titulo do card. */
  days?: number;
}

export function MetricCard({ data, days = 14 }: MetricCardProps) {
  const title = `Aderência (${days} dias)`;
  if (data.scheduled === 0) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-base">{title}</CardTitle>
          <CardDescription>
            Sem medicamentos agendados nesta janela.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }
  const pct = Math.round(data.adherence_frac * 100);
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{title}</CardTitle>
        <CardDescription>
          {data.taken} de {data.scheduled} doses tomadas.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <div className="flex items-baseline gap-2">
          <span className={cn("text-4xl font-semibold", colorFor(pct))}>
            {pct}%
          </span>
          <span className="text-sm text-muted-foreground">do agendado</span>
        </div>
        <div
          className="mt-3 h-2 w-full overflow-hidden rounded-full bg-muted"
          role="progressbar"
          aria-valuenow={pct}
          aria-valuemin={0}
          aria-valuemax={100}
        >
          <div
            className={cn("h-full transition-all", barColorFor(pct))}
            style={{ width: `${pct}%` }}
          />
        </div>
      </CardContent>
    </Card>
  );
}

function colorFor(pct: number): string {
  if (pct >= 90) return "text-emerald-700";
  if (pct >= 70) return "text-amber-700";
  return "text-red-700";
}

function barColorFor(pct: number): string {
  if (pct >= 90) return "bg-emerald-500";
  if (pct >= 70) return "bg-amber-500";
  return "bg-red-500";
}
