import Link from "next/link";

import { PreferencesForm } from "@/components/forms/PreferencesForm";
import { getMe } from "@/lib/api/auth";
import { getSessionCookieHeader } from "@/lib/server-cookie";

export const dynamic = "force-dynamic";

export default async function UserPreferencesPage() {
  const cookieHeader = getSessionCookieHeader();
  const user = await getMe(cookieHeader);

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <Link
        href="/dashboard"
        className="text-sm text-muted-foreground hover:text-foreground"
      >
        ← Voltar ao painel
      </Link>
      <header>
        <h1 className="text-3xl font-semibold tracking-tight">Preferencias</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Como o assistente deve te tratar e te avisar.
        </p>
      </header>
      <PreferencesForm user={user} />
    </div>
  );
}
