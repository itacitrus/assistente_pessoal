"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { AlertCircle, AlertTriangle, Check, Info } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import type { AlertSeverity, AlertSummary } from "@/types/api";
import { ApiError } from "@/lib/api";
import { reviewDependentAlert } from "@/lib/api/family";
import { cn } from "@/lib/utils";

export interface AlertListProps {
  alerts: AlertSummary[];
  dependentId: number;
}

export function AlertList({ alerts, dependentId }: AlertListProps) {
  // Backend Go pode mandar slice nil como `null` no JSON — guarda defensiva.
  const list = alerts ?? [];
  if (list.length === 0) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Alertas em aberto</CardTitle>
          <CardDescription>
            Nenhum alerta aberto nas últimas semanas.
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
          Sinalizações que o Zello identificou. Marque como revisado quando já
          tiver olhado.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {list.map((a) => (
          <AlertRow key={a.id} alert={a} dependentId={dependentId} />
        ))}
      </CardContent>
    </Card>
  );
}

function AlertRow({
  alert,
  dependentId,
}: {
  alert: AlertSummary;
  dependentId: number;
}) {
  const router = useRouter();
  const cfg = SEVERITY_CONFIG[alert.severity] ?? SEVERITY_CONFIG.info;
  const Icon = cfg.Icon;

  const [open, setOpen] = React.useState(false);
  const [note, setNote] = React.useState("");
  const [status, setStatus] = React.useState<"idle" | "saving" | "error">(
    "idle",
  );
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  async function handleConfirm() {
    setStatus("saving");
    setErrorMsg(null);
    try {
      await reviewDependentAlert(dependentId, alert.id, note.trim());
      // Recarrega os dados do servidor — o alerta revisado sai da lista.
      router.refresh();
    } catch (err) {
      setStatus("error");
      setErrorMsg(
        err instanceof ApiError
          ? err.message
          : "Não consegui salvar agora. Tente novamente em instantes.",
      );
    }
  }

  return (
    <div className={cn("rounded-md border p-3", cfg.borderClass)}>
      <div className="flex gap-3">
        <Icon className={cn("mt-0.5 h-5 w-5 shrink-0", cfg.iconClass)} />
        <div className="flex-1">
          <p className="text-sm font-medium leading-snug">
            {humanizePolicy(alert.policy_name)}
          </p>
          <p className="mt-1 text-xs text-muted-foreground">
            {humanizeAge(alert.created_at)}
          </p>
        </div>
        {!open ? (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => setOpen(true)}
          >
            <Check className="h-4 w-4" aria-hidden />
            Marcar como revisado
          </Button>
        ) : null}
      </div>

      {open ? (
        <div className="mt-3 space-y-2 border-t pt-3">
          <label
            htmlFor={`note-${alert.id}`}
            className="text-xs font-medium text-muted-foreground"
          >
            Anotação (opcional)
          </label>
          <textarea
            id={`note-${alert.id}`}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            maxLength={500}
            rows={2}
            placeholder="Ex.: liguei, está tudo bem."
            className="w-full resize-none rounded-md border bg-background p-2 text-sm outline-none focus:ring-2 focus:ring-[--zello-emerald]/30"
            disabled={status === "saving"}
          />
          <div className="flex items-center gap-2">
            <Button
              type="button"
              size="sm"
              onClick={handleConfirm}
              disabled={status === "saving"}
            >
              {status === "saving" ? "Salvando..." : "Confirmar revisão"}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => {
                setOpen(false);
                setNote("");
                setStatus("idle");
                setErrorMsg(null);
              }}
              disabled={status === "saving"}
            >
              Cancelar
            </Button>
          </div>
          {status === "error" && errorMsg ? (
            <Alert variant="destructive">
              <AlertDescription>{errorMsg}</AlertDescription>
            </Alert>
          ) : null}
        </div>
      ) : null}
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
    severe_signal_safety_net: "Sinal preocupante (revisão)",
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
  if (diffMin < 1) return "agora há pouco";
  if (diffMin < 60) return `há ${diffMin} min`;
  const diffH = Math.round(diffMin / 60);
  if (diffH < 24) return `há ${diffH}h`;
  const diffD = Math.round(diffH / 24);
  if (diffD < 7) return `há ${diffD} dia${diffD > 1 ? "s" : ""}`;
  return then.toLocaleDateString("pt-BR");
}
