import Link from "next/link";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { DependentList } from "@/components/family/DependentList";
import { ApiError } from "@/lib/api";
import { getMe } from "@/lib/api/auth";
import { listDependents } from "@/lib/api/family";
import { getSessionCookieHeader } from "@/lib/server-cookie";
import type { DependentEntry, User } from "@/types/api";

export const dynamic = "force-dynamic";

export default async function DashboardHome() {
  const cookieHeader = getSessionCookieHeader();
  const me = await getMe(cookieHeader);

  let dependents: DependentEntry[] = [];
  if (me.type === "responsavel") {
    try {
      const res = await listDependents(cookieHeader);
      dependents = res.dependents;
    } catch (err) {
      // 403 not_responsavel ou outros erros nao bloqueiam o dashboard
      if (!(err instanceof ApiError) || err.status !== 403) {
        throw err;
      }
    }
  }

  return (
    <div className="space-y-8">
      <section>
        <h1 className="text-3xl font-semibold tracking-tight">
          Ola, {firstName(me)}.
        </h1>
        <p className="mt-2 text-muted-foreground">
          Bem-vindo de volta ao painel.
        </p>
      </section>

      <AgendaCard user={me} />

      {me.type === "responsavel" && (
        <section className="space-y-4">
          <header className="flex items-center justify-between">
            <h2 className="text-2xl font-semibold tracking-tight">
              Pessoas que voce cuida
            </h2>
          </header>
          <DependentList dependents={dependents} />
        </section>
      )}

      {me.type === "comum" && (
        <Card>
          <CardHeader>
            <CardTitle className="text-lg">Quer cuidar de alguem?</CardTitle>
            <CardDescription>
              Voce pode atualizar seu cadastro para ser responsavel de um
              familiar.
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Button asChild variant="outline">
              <Link href="/dashboard/preferences">Editar conta</Link>
            </Button>
          </CardContent>
        </Card>
      )}
    </div>
  );
}

function firstName(u: User): string {
  return u.name.split(" ")[0] || u.name;
}

function AgendaCard({ user }: { user: User }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">Sua agenda</CardTitle>
        <CardDescription>
          {user.google_connected
            ? "Conectada ao Google Calendar."
            : "Ainda nao conectada ao Google Calendar."}
        </CardDescription>
      </CardHeader>
      <CardContent>
        <p className="text-sm text-muted-foreground">
          A conexao do Google e feita pelo proprio bot no WhatsApp. Se
          precisar reconectar, peca o link la.
        </p>
      </CardContent>
    </Card>
  );
}
