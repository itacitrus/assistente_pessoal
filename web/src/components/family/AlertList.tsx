import { AlertCircle, AlertTriangle, Info } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import type { AlertSeverity, AlertSummary } from "@/types/api";
import { cn } from "@/lib/utils";

export interface AlertListProps {
  alerts: AlertSummary[];
}

export function AlertList({ alerts }: AlertListProps) {
  // Backend Go pode mandar slice nil como `null` no JSON — guarda defensiva.
  const list = alerts ?? [];
  if (list.length === 0) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Alertas em aberto</CardTitle>
          <CardDescription>
            Nenhum alerta aberto nas ultimas semanas.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Alertas em aberto</CardTitle>
        <CardDescription>
          Sinalizacoes que o Zello identificou.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {list.map((a) => (
          <AlertRow key={a.id} alert={a} />
        ))}
      </CardContent>
    </Card>
  );
}

function AlertRow({ alert }: { alert: AlertSummary }) {
  const cfg = SEVERITY_CONFIG[alert.severity] ?? SEVERITY_CONFIG.info;
  const Icon = cfg.Icon;
  return (
    <div className={cn("flex gap-3 rounded-md border p-3", cfg.borderClass)}>
      <Icon className={cn("mt-0.5 h-5 w-5 shrink-0", cfg.iconClass)} />
      <div className="flex-1">
        <p className="text-sm font-medium leading-snug">
          {humanizePolicy(alert.policy_name)}
        </p>
        <p className="mt-1 text-xs text-muted-foreground">
          {humanizeAge(alert.created_at)}
        </p>
      </div>
    </div>
  );
}

const SEVERITY_CONFIG: Record<
  AlertSeverity,
  {
    Icon: typeof Info;
    iconClass: string;
    borderClass: string;
  }
> = {
  info: {
    Icon: Info,
    iconClass: "text-slate-600",
    borderClass: "border-slate-200",
  },
  warn: {
    Icon: AlertTriangle,
    iconClass: "text-amber-700",
    borderClass: "border-amber-300",
  },
  critical: {
    Icon: AlertCircle,
    iconClass: "text-red-700",
    borderClass: "border-red-300",
  },
};

/**
 * Mapa pt-BR para `policy_name` do backend. Backend explicitamente NAO expoe
 * a `message` do alerta (privacidade) — entao o label aqui descreve a
 * categoria do alerta, sem detalhes de conteudo.
 */
function humanizePolicy(policy: string): string {
  const map: Record<string, string> = {
    medication_miss: "Dose perdida",
    inactivity: "Sem responder",
    severe_signal: "Sinal preocupante",
    severe_signal_safety_net: "Sinal preocupante (revisao)",
  };
  return map[policy] ?? "Alerta";
}

/**
 * Idade humanizada do alerta. Sem dependencia em libs externas — texto curto
 * em pt-BR, foco em "ha quanto tempo aberto".
 */
function humanizeAge(iso: string): string {
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
