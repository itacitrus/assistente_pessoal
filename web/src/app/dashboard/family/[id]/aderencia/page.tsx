import Link from "next/link";
import { notFound } from "next/navigation";
import { Activity } from "lucide-react";

import { IntakeHistoryList } from "@/components/family/IntakeHistoryList";
import { MetricCard } from "@/components/family/MetricCard";
import { ApiError } from "@/lib/api";
import {
  getDependentIntakes,
  getDependentStatus,
  listDependents,
} from "@/lib/api/family";
import { getSessionCookieHeader } from "@/lib/server-cookie";
import type { IntakeEntry } from "@/types/api";

export const dynamic = "force-dynamic";

interface PageProps {
  params: { id: string };
}

export default async function AderenciaPage({ params }: PageProps) {
  const id = parseInt(params.id, 10);
  if (Number.isNaN(id)) notFound();

  const cookieHeader = getSessionCookieHeader();

  let status;
  let intakes: IntakeEntry[] = [];
  let days = 14;
  let dependentName = "essa pessoa";
  try {
    const [s, intk, deps] = await Promise.all([
      getDependentStatus(id, { cookieHeader }),
      getDependentIntakes(id, { days: 14, cookieHeader }),
      listDependents(cookieHeader),
    ]);
    status = s;
    intakes = intk.intakes;
    days = intk.days;
    const found = (deps.dependents ?? []).find((d) => d.user.id === id);
    if (found) dependentName = found.user.name;
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      notFound();
    }
    throw err;
  }

  return (
    <div className="mx-auto max-w-2xl space-y-8">
      <Link
        href={`/dashboard/family/${id}`}
        className="text-sm text-muted-foreground hover:text-foreground"
      >
        ← Voltar para detalhes
      </Link>

      <header className="animate-rise">
        <div className="flex items-center gap-2">
          <Activity className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
          <p className="text-sm font-medium text-[--zello-emerald]">Aderência</p>
        </div>
        <h1 className="mt-1 font-display text-3xl font-semibold tracking-tight">
          Doses de {dependentName}
        </h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Cada dose agendada nos últimos {days} dias e se foi tomada, dia a dia.
        </p>
      </header>

      <MetricCard data={status.medication} days={days} />

      <section className="space-y-4 animate-rise" style={{ animationDelay: "60ms" }}>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h2 className="font-display text-xl font-semibold tracking-tight">
            Dose a dose
          </h2>
          <Legend />
        </div>
        <IntakeHistoryList
          intakes={intakes}
          emptyText="Ainda não há doses registradas nesta janela."
        />
      </section>
    </div>
  );
}

/** Legenda compacta das cores de status usadas na lista. */
function Legend() {
  const items: { label: string; dot: string }[] = [
    { label: "Tomada", dot: "bg-emerald-500" },
    { label: "Não tomada", dot: "bg-red-500" },
    { label: "Pulada", dot: "bg-amber-500" },
    { label: "Sem confirmação exigida", dot: "bg-muted-foreground/30" },
  ];
  return (
    <ul className="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
      {items.map((it) => (
        <li key={it.label} className="flex items-center gap-1.5">
          <span className={`h-2 w-2 rounded-full ${it.dot}`} aria-hidden />
          {it.label}
        </li>
      ))}
    </ul>
  );
}
