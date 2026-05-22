import Link from "next/link";
import { Pill } from "lucide-react";

import { MedicationCard } from "@/components/family/MedicationCard";
import { AddMedicationDisclosure } from "@/components/forms/AddMedicationDisclosure";
import { Card, CardContent } from "@/components/ui/card";
import { getMyMedications } from "@/lib/api/me";
import { getSessionCookieHeader } from "@/lib/server-cookie";
import type { MedicationItem } from "@/types/api";

export const dynamic = "force-dynamic";

export default async function MeusRemediosPage() {
  const cookieHeader = getSessionCookieHeader();

  let medications: MedicationItem[] = [];
  try {
    const res = await getMyMedications(cookieHeader);
    medications = res.medications ?? [];
  } catch {
    // Falha de leitura não derruba a página — mostra vazio e o form pra cadastrar.
    medications = [];
  }

  return (
    <div className="mx-auto max-w-2xl space-y-8">
      <Link
        href="/dashboard"
        className="text-sm text-muted-foreground hover:text-foreground"
      >
        ← Voltar ao painel
      </Link>

      <header className="animate-rise">
        <div className="flex items-center gap-2">
          <Pill className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
          <p className="text-sm font-medium text-[--zello-emerald]">Remédios</p>
        </div>
        <h1 className="mt-1 font-display text-3xl font-semibold tracking-tight">
          Meus remédios
        </h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Cadastre os seus remédios para o Zello lembrar você na hora certa —
          contínuos ou por um período.
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
                Você ainda não cadastrou nenhum remédio. Cadastre o primeiro pra
                o Zello lembrar na hora certa.
              </p>
            </CardContent>
          </Card>
        ) : (
          <div className="space-y-3">
            {medications.map((m) => (
              <MedicationCard
                key={m.id}
                target={{ kind: "self" }}
                medication={m}
              />
            ))}
          </div>
        )}
      </section>

      <section
        className="space-y-4 animate-rise"
        style={{ animationDelay: "120ms" }}
      >
        <AddMedicationDisclosure target={{ kind: "self" }} />
      </section>
    </div>
  );
}
