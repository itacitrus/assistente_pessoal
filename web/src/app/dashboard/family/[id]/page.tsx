import Link from "next/link";
import { notFound } from "next/navigation";

import { AlertList } from "@/components/family/AlertList";
import { ConnectDependentGoogleButton } from "@/components/family/ConnectDependentGoogleButton";
import { DependentDangerZone } from "@/components/family/DependentDangerZone";
import { DependentDataForm } from "@/components/family/DependentDataForm";
import { MetricCard } from "@/components/family/MetricCard";
import { StatusHeader } from "@/components/family/StatusHeader";
import { PendingAutoRefresh } from "@/components/PendingAutoRefresh";
import { DependentRefreshButton } from "@/components/RefreshPanelButton";
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
  let dependentName = "";
  let dependentPhone = "";
  let dependentGoogleConnected = false;
  let dependentActive = true;
  let medications: MedicationItem[] = [];
  try {
    const [s, deps, meds] = await Promise.all([
      getDependentStatus(id, { cookieHeader }),
      listDependents(cookieHeader),
      // Medicamentos sao apenas um resumo aqui — falha nao derruba a pagina.
      safeMedications(() => getDependentMedications(id, cookieHeader)),
    ]);
    status = s;
    const entry = deps.dependents.find((d) => d.user.id === id);
    relationship = entry?.link.relationship;
    dependentName = entry?.user.name ?? "";
    dependentPhone = entry?.user.phone_number ?? "";
    dependentGoogleConnected = entry?.user.google_connected ?? false;
    dependentActive = entry?.user.is_active ?? true;
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
      <div className="flex items-start justify-between gap-3">
        <Link
          href="/dashboard"
          className="text-sm text-muted-foreground hover:text-foreground"
        >
          ← Voltar ao painel
        </Link>
        <DependentRefreshButton
          dependentId={Number(id)}
          lastUpdated={status.synthesis_generated_at}
        />
      </div>

      <StatusHeader status={status} relationship={relationship} />

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
        <MetricCard
          data={status.medication}
          days={status.days}
          detailHref={`/dashboard/family/${id}/aderencia`}
        />
        <SynthesisCard
          synthesis={status.synthesis}
          available={status.synthesis_available !== false}
        />
      </div>
      <PendingAutoRefresh pending={status.synthesis_available === false} />

      <AlertList alerts={status.alerts_open} dependentId={Number(id)} />

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

      <div className="space-y-3 rounded-lg border bg-card p-5 shadow-warm">
        <div>
          <h2 className="font-display text-lg font-semibold tracking-tight">
            Agenda do Google
          </h2>
          <p className="text-sm text-muted-foreground">
            {dependentGoogleConnected
              ? "A agenda está conectada. O Zello usa ela para lembrar dos compromissos."
              : `O Zello envia um link no WhatsApp de ${dependentName.split(" ")[0] || "do dependente"} para conectar a agenda. Quem autoriza é a própria pessoa, no aparelho dela.`}
          </p>
        </div>
        <ConnectDependentGoogleButton
          dependentId={id}
          dependentName={dependentName}
          connected={dependentGoogleConnected}
        />
      </div>

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

      <DependentDataForm
        dependentId={id}
        initialName={dependentName}
        initialPhoneE164={dependentPhone}
      />

      <DependentDangerZone
        dependentId={id}
        dependentName={dependentName}
        initialActive={dependentActive}
      />
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
