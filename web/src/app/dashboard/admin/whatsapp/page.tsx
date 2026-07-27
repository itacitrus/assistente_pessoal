import { redirect } from "next/navigation";
import Link from "next/link";
import { ArrowLeft, Smartphone } from "lucide-react";

import { WhatsAppPairing } from "@/components/admin/WhatsAppPairing";
import { ApiError } from "@/lib/api";
import { getMe } from "@/lib/api/auth";
import { getPairingStatus, type PairingStatus } from "@/lib/api/pairing";
import { getSessionCookieHeader } from "@/lib/server-cookie";

export const dynamic = "force-dynamic";

/**
 * Página admin de pareamento do WhatsApp. Gera código de 8 dígitos (pareamento
 * por número) e QR code para vincular o bot a uma conta do WhatsApp — útil
 * quando o vínculo é perdido (ex: celular formatado). Acesso restrito ao
 * operador (allowlist ADMIN_PHONES); o gate real é do backend (403).
 */
export default async function WhatsAppPairingPage() {
  const cookieHeader = getSessionCookieHeader();

  const me = await getMe(cookieHeader);
  if (!me.is_admin) {
    redirect("/dashboard");
  }

  let initial: PairingStatus | null = null;
  try {
    initial = await getPairingStatus(cookieHeader);
  } catch (err) {
    if (!(err instanceof ApiError)) throw err;
    // 503 (pareamento indisponível) ou falha de rede — o cliente reconsulta.
  }

  return (
    <div className="space-y-8">
      <section className="animate-rise">
        <Link
          href="/dashboard/admin"
          className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" aria-hidden />
          Voltar ao admin
        </Link>
        <div className="mt-3 flex items-center gap-2">
          <Smartphone className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
          <p className="text-sm font-medium text-[--zello-emerald]">
            Conexão do WhatsApp
          </p>
        </div>
        <h1 className="mt-1 font-display text-3xl font-semibold tracking-tight text-foreground sm:text-4xl">
          Parear o WhatsApp do bot
        </h1>
        <p className="mt-3 max-w-prose text-base text-muted-foreground">
          Vincule o bot a uma conta do WhatsApp gerando um código de 8 dígitos
          ou um QR code. Use quando a conexão for perdida — por exemplo, depois
          de trocar ou formatar o celular.
        </p>
      </section>

      <WhatsAppPairing initial={initial} />
    </div>
  );
}
