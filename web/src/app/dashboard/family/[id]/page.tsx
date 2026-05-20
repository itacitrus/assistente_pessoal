import Link from "next/link";
import { notFound } from "next/navigation";

import { AlertList } from "@/components/family/AlertList";
import { MetricCard } from "@/components/family/MetricCard";
import { StatusHeader } from "@/components/family/StatusHeader";
import { SynthesisCard } from "@/components/family/SynthesisCard";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { getDependentStatus, listDependents } from "@/lib/api/family";
import { getSessionCookieHeader } from "@/lib/server-cookie";

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
  try {
    const [s, deps] = await Promise.all([
      getDependentStatus(id, { cookieHeader }),
      listDependents(cookieHeader),
    ]);
    status = s;
    relationship = deps.dependents.find((d) => d.user.id === id)?.link
      .relationship;
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      notFound();
    }
    throw err;
  }

  return (
    <div className="space-y-6">
      <Link
        href="/dashboard"
        className="text-sm text-muted-foreground hover:text-foreground"
      >
        ← Voltar ao painel
      </Link>

      <StatusHeader status={status} relationship={relationship} />

      <div className="grid gap-4 md:grid-cols-2">
        <MetricCard data={status.medication} days={status.days} />
        <SynthesisCard synthesis={status.synthesis} />
      </div>

      <AlertList alerts={status.alerts_open} />

      <div className="flex flex-wrap gap-3">
        <Button asChild>
          <Link href={`/dashboard/family/${id}/evolucao`}>
            Ver evolucao psicologica
          </Link>
        </Button>
        <Button asChild variant="outline">
          <Link href={`/dashboard/family/${id}/preferences`}>
            Preferencias de notificacao
          </Link>
        </Button>
      </div>
    </div>
  );
}
