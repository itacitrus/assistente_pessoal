import Link from "next/link";
import { notFound } from "next/navigation";

import { NotifyPreferencesForm } from "@/components/forms/NotifyPreferencesForm";
import { ApiError } from "@/lib/api";
import { listDependents } from "@/lib/api/family";
import { getSessionCookieHeader } from "@/lib/server-cookie";

export const dynamic = "force-dynamic";

interface PageProps {
  params: { id: string };
}

export default async function NotifyPreferencesPage({ params }: PageProps) {
  const id = parseInt(params.id, 10);
  if (Number.isNaN(id)) notFound();

  const cookieHeader = getSessionCookieHeader();
  let dependent;
  try {
    const res = await listDependents(cookieHeader);
    dependent = res.dependents.find((d) => d.user.id === id);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      notFound();
    }
    throw err;
  }
  if (!dependent) notFound();

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <Link
        href={`/dashboard/family/${id}`}
        className="text-sm text-muted-foreground hover:text-foreground"
      >
        ← Voltar para detalhes
      </Link>
      <header>
        <h1 className="text-3xl font-semibold tracking-tight">
          Notificacoes — {dependent.user.name}
        </h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Escolha quais avisos voce quer receber. Voce pode mudar a qualquer
          momento.
        </p>
      </header>
      <NotifyPreferencesForm link={dependent.link} />
    </div>
  );
}
