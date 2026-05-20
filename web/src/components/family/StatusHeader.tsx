import {
  ArrowDown,
  ArrowRight,
  ArrowUp,
  ArrowUpDown,
  HelpCircle,
} from "lucide-react";

import type { DependentStatus, Tendencia } from "@/types/api";
import { cn } from "@/lib/utils";

export interface StatusHeaderProps {
  status: DependentStatus;
  /**
   * Relacao parentesco (link.relationship) — vem do listDependents/sumario;
   * o endpoint /status nao reexpoe pra evitar duplicacao de payload.
   */
  relationship?: string;
}

export function StatusHeader({ status, relationship }: StatusHeaderProps) {
  const trend: Tendencia = status.synthesis.tendencia;
  const lastMessage = status.last_user_message_at
    ? formatRelative(status.last_user_message_at)
    : daysSince(status.days_since_last_talk);

  return (
    <header className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
      <div>
        <h1 className="text-3xl font-semibold tracking-tight">
          {status.dependent.name}
        </h1>
        <p className="text-sm text-muted-foreground">
          Ultima conversa: {lastMessage}
        </p>
        {relationship && (
          <p className="text-sm capitalize text-muted-foreground">
            {relationship}
          </p>
        )}
      </div>
      <TrendBadge trend={trend} />
    </header>
  );
}

function TrendBadge({ trend }: { trend: Tendencia }) {
  const cfg = TREND_CONFIG[trend];
  const Icon = cfg.icon;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-2 rounded-full px-3 py-1 text-sm font-medium",
        cfg.className,
      )}
    >
      <Icon className="h-4 w-4" />
      {cfg.label}
    </span>
  );
}

const TREND_CONFIG: Record<
  Tendencia,
  { label: string; icon: typeof ArrowUp; className: string }
> = {
  melhorando: {
    label: "Em melhora",
    icon: ArrowUp,
    className: "bg-emerald-100 text-emerald-900",
  },
  estavel: {
    label: "Estavel",
    icon: ArrowRight,
    className: "bg-sky-100 text-sky-900",
  },
  piorando: {
    label: "Em piora",
    icon: ArrowDown,
    className: "bg-amber-100 text-amber-900",
  },
  instavel: {
    label: "Oscilando",
    icon: ArrowUpDown,
    className: "bg-amber-100 text-amber-900",
  },
  indeterminado: {
    label: "Sem dados suficientes",
    icon: HelpCircle,
    className: "bg-muted text-muted-foreground",
  },
};

function formatRelative(iso: string): string {
  const then = new Date(iso);
  const now = new Date();
  const diffMs = now.getTime() - then.getTime();
  const diffMin = Math.round(diffMs / 60000);
  if (diffMin < 1) return "agora ha pouco";
  if (diffMin < 60) return `ha ${diffMin} min`;
  const diffH = Math.round(diffMin / 60);
  if (diffH < 24) return `ha ${diffH}h`;
  const diffD = Math.round(diffH / 24);
  if (diffD < 7) return `ha ${diffD} dia${diffD > 1 ? "s" : ""}`;
  return then.toLocaleDateString("pt-BR");
}

function daysSince(days: number): string {
  if (days <= 0) return "hoje";
  if (days === 1) return "ha 1 dia";
  if (days >= 999) return "sem registro recente";
  return `ha ${days} dias`;
}
