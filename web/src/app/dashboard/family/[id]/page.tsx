import Link from "next/link";
import { notFound } from "next/navigation";

import { AlertList } from "@/components/family/AlertList";
import { MetricCard } from "@/components/family/MetricCard";
import { StatusHeader } from "@/components/family/StatusHeader";
import { SynthesisCard } from "@/components/family/SynthesisCard";
import { Pill } from "lucide-react";

import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import {
  getDependentMedications,
  getDependentStatus,
  listDependents,
} from "@/lib/api/family";
import { getSessionCookieHeader } from "@/lib/server-cookie";
import type { MedicationItem } from "@/types/api";

export const dynamic = "force-dynamic";

interface PageProps {
  params: { id: string };
}

export default async function DependentDetailPage({ params }: PageProps) {
  const id = parseInt(params.id, 10);
  if (Number.isNaN(id)) notFound();

  const cookieHeader = getSessionCookieHeader();

  // Buscamos /status (snapshot) + lista de dependentes em paralelo. A lista
  // ja eh carregada no dashboard pai, mas precisamos do `link.relationship`
  // aqui (o /status nao reexpoe o link) — ler de novo eh barato (no cache do
  // backend) e mantem este page auto-contido.
  let status;
  let relationship: string | undefined;
  let medications: MedicationItem[] = [];
  try {
    const [s, deps, meds] = await Promise.all([
      getDependentStatus(id, { cookieHeader }),
      listDependents(cookieHeader),
      // Medicamentos sao apenas um resumo aqui — falha nao derruba a pagina.
      safeMedications(() => getDependentMedications(id, cookieHeader)),
    ]);
    status = s;
    relationship = deps.dependents.find((d) => d.user.id === id)?.link
      .relationship;
    medications = meds;
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      notFound();
    }
    throw err;
  }

  const activeMeds = medications.filter((m) => m.active).length;

  return (
    <div className="space-y-6">
      <Link
        href="/dashboard"
        className="text-sm text-muted-foreground hover:text-foreground"
      >
        ← Voltar ao painel
      </Link>

      <StatusHeader status={status} relationship={relationship} />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <MetricCard data={status.medication} days={status.days} />
        <SynthesisCard synthesis={status.synthesis} />
      </div>

      <AlertList alerts={status.alerts_open} />

      <Link
        href={`/dashboard/family/${id}/medicamentos`}
        className="flex items-center gap-3 rounded-lg border bg-card p-4 shadow-warm transition-shadow hover:shadow-warm-lg"
      >
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-[--zello-emerald]/10 text-[--zello-emerald]">
          <Pill className="h-5 w-5" aria-hidden />
        </div>
        <div className="min-w-0 flex-1">
          <p className="font-medium text-foreground">Remédios</p>
          <p className="text-sm text-muted-foreground">
            {medicationsSummary(activeMeds)}
          </p>
        </div>
        <span className="text-sm text-muted-foreground" aria-hidden>
          →
        </span>
      </Link>

      <div className="flex flex-wrap gap-3">
        <Button asChild>
          <Link href={`/dashboard/family/${id}/evolucao`}>
            Ver evolução psicológica
          </Link>
        </Button>
        <Button asChild variant="outline">
          <Link href={`/dashboard/family/${id}/medicamentos`}>
            Gerenciar remédios
          </Link>
        </Button>
        <Button asChild variant="outline">
          <Link href={`/dashboard/family/${id}/preferences`}>
            Preferências de notificação
          </Link>
        </Button>
      </div>
    </div>
  );
}

function medicationsSummary(active: number): string {
  if (active === 0) return "Nenhum remédio cadastrado — toque para adicionar.";
  if (active === 1) return "1 remédio ativo. Toque para gerenciar.";
  return `${active} remédios ativos. Toque para gerenciar.`;
}

/** Busca medicamentos com fallback vazio em qualquer falha. */
async function safeMedications(
  fn: () => Promise<{ medications: MedicationItem[] }>,
): Promise<MedicationItem[]> {
  try {
    const res = await fn();
    return res.medications ?? [];
  } catch {
    return [];
  }
}
