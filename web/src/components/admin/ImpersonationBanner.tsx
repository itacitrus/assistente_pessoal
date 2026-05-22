"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Eye, Loader2, X } from "lucide-react";

import { stopImpersonation } from "@/lib/api/admin";

export interface ImpersonationBannerProps {
  /** Nome da pessoa cujo painel o admin esta visualizando. */
  name: string;
}

/**
 * Faixa fixa exibida enquanto um admin esta "vendo como" outra pessoa. Deixa
 * explicito de quem eh o painel (evita o admin achar que esta no proprio) e
 * oferece a saida rapida. Sair limpa a impersonacao no servidor e volta pra
 * area admin.
 */
export function ImpersonationBanner({ name }: ImpersonationBannerProps) {
  const router = useRouter();
  const [leaving, setLeaving] = React.useState(false);

  async function handleStop() {
    setLeaving(true);
    try {
      await stopImpersonation();
      router.push("/dashboard/admin");
      router.refresh();
    } catch {
      setLeaving(false);
    }
  }

  return (
    <div className="border-b border-[--zello-amber]/30 bg-[--zello-amber]/15">
      <div className="container flex flex-wrap items-center justify-between gap-2 py-2.5 text-sm">
        <span className="flex items-center gap-2 font-medium text-[--zello-emerald-deep]">
          <Eye className="h-4 w-4 shrink-0" aria-hidden />
          Você está vendo o painel de {name} (modo administrador).
        </span>
        <button
          type="button"
          onClick={handleStop}
          disabled={leaving}
          className="inline-flex items-center gap-1 rounded-md font-medium text-[--zello-emerald-deep] underline-offset-4 hover:underline disabled:opacity-60"
        >
          {leaving ? (
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
          ) : (
            <X className="h-4 w-4" aria-hidden />
          )}
          Sair da visão
        </button>
      </div>
    </div>
  );
}
