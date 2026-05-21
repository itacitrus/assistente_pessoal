import Link from "next/link";
import { notFound } from "next/navigation";
import { Pill } from "lucide-react";

import { MedicationCard } from "@/components/family/MedicationCard";
import { MedicationForm } from "@/components/forms/MedicationForm";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { ApiError } from "@/lib/api";
import { getDependentMedications, listDependents } from "@/lib/api/family";
import { getSessionCookieHeader } from "@/lib/server-cookie";
import type { MedicationItem } from "@/types/api";

export const dynamic = "force-dynamic";

interface PageProps {
  params: { id: string };
}

export default async function MedicamentosPage({ params }: PageProps) {
  const id = parseInt(params.id, 10);
  if (Number.isNaN(id)) notFound();

  const cookieHeader = getSessionCookieHeader();

  // Lista de remedios + nome do dependente (pra contextualizar o titulo).
  // O endpoint de medicamentos nao reexpoe o nome; lemos a lista de
  // dependentes (barata, cacheada no backend) em paralelo.
  let medications: MedicationItem[] = [];
  let dependentName = "essa pessoa";
  try {
    const [meds, deps] = await Promise.all([
      getDependentMedications(id, cookieHeader),
      listDependents(cookieHeader),
    ]);
    medications = meds.medications ?? [];
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
          <Pill className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
          <p className="text-sm font-medium text-[--zello-emerald]">
            Remédios
          </p>
        </div>
        <h1 className="mt-1 font-display text-3xl font-semibold tracking-tight">
          Remédios de {dependentName}
        </h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Cadastre os remédios para o Zello lembrar na hora certa, todos os
          dias.
        </p>
      </header>

      <section
        className="space-y-4 animate-rise"
        style={{ animationDelay: "60ms" }}
        aria-labelledby="lista-remedios"
      >
        <h2
          id="lista-remedios"
          className="font-display text-xl font-semibold tracking-tight"
        >
          Remédios cadastrados
        </h2>
        {medications.length === 0 ? (
          <Card className="border-dashed bg-muted/30 shadow-warm">
            <CardContent className="flex flex-col items-center gap-3 p-8 text-center">
              <div className="flex h-12 w-12 items-center justify-center rounded-full bg-[--zello-emerald]/10 text-[--zello-emerald]">
                <Pill className="h-6 w-6" aria-hidden />
              </div>
              <p className="max-w-md text-base text-muted-foreground">
                Nenhum remédio cadastrado ainda. Cadastre o primeiro pra o Zello
                lembrar na hora certa.
              </p>
            </CardContent>
          </Card>
        ) : (
          <div className="space-y-3">
            {medications.map((m) => (
              <MedicationCard key={m.id} dependentId={id} medication={m} />
            ))}
          </div>
        )}
      </section>

      <section
        className="space-y-4 animate-rise"
        style={{ animationDelay: "120ms" }}
        aria-labelledby="add-remedio"
      >
        <Card className="shadow-warm">
          <CardHeader>
            <CardTitle id="add-remedio" className="text-lg">
              Adicionar remédio
            </CardTitle>
            <CardDescription>
              Nome, dose, horários e em quais dias o Zello deve lembrar.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <MedicationForm dependentId={id} />
          </CardContent>
        </Card>
      </section>
    </div>
  );
}
