import Link from "next/link";
import { notFound } from "next/navigation";

import { PsychTimeline } from "@/components/family/PsychTimeline";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { getDependentTimeline } from "@/lib/api/family";
import { getSessionCookieHeader } from "@/lib/server-cookie";

export const dynamic = "force-dynamic";

interface PageProps {
  params: { id: string };
  searchParams: { dias?: string };
}

const ALLOWED_WINDOWS = [30, 60, 90, 180] as const;

export default async function EvolucaoPage({
  params,
  searchParams,
}: PageProps) {
  const id = parseInt(params.id, 10);
  if (Number.isNaN(id)) notFound();

  const requested = parseInt(searchParams.dias ?? "90", 10);
  const days = ALLOWED_WINDOWS.includes(
    requested as (typeof ALLOWED_WINDOWS)[number],
  )
    ? requested
    : 90;

  const cookieHeader = getSessionCookieHeader();
  let timeline;
  let dependentName = "Dependente";
  try {
    // Timeline ja vem com `dependent: {id, name}` — nao precisamos buscar
    // /status so para pegar o nome.
    timeline = await getDependentTimeline(id, { days, cookieHeader });
    dependentName = timeline.dependent.name;
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      notFound();
    }
    throw err;
  }

  return (
    <div className="space-y-6">
      <Link
        href={`/dashboard/family/${id}`}
        className="text-sm text-muted-foreground hover:text-foreground"
      >
        ← Voltar para detalhes
      </Link>

      <header className="flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="text-3xl font-semibold tracking-tight">
            Evolução — {dependentName}
          </h1>
          <p className="mt-2 text-sm text-muted-foreground">
            Sinais agregados a partir das conversas. Pontos com confiança
            baixa aparecem mais transparentes.
          </p>
        </div>
        <WindowSelector currentDays={days} dependentId={id} />
      </header>

      <PsychTimeline snapshots={timeline.snapshots} />
    </div>
  );
}

function WindowSelector({
  currentDays,
  dependentId,
}: {
  currentDays: number;
  dependentId: number;
}) {
  return (
    <div className="flex flex-wrap gap-2">
      {ALLOWED_WINDOWS.map((d) => (
        <Button
          key={d}
          asChild
          variant={d === currentDays ? "default" : "outline"}
          size="sm"
        >
          <Link href={`/dashboard/family/${dependentId}/evolucao?dias=${d}`}>
            {d} dias
          </Link>
        </Button>
      ))}
    </div>
  );
}
