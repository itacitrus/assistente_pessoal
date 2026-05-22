import Link from "next/link";
import { CalendarClock } from "lucide-react";

import { AgendaCalendar } from "@/components/me/AgendaCalendar";

export const dynamic = "force-dynamic";

export default function AgendaPage() {
  return (
    <div className="mx-auto max-w-3xl space-y-8">
      <Link
        href="/dashboard"
        className="text-sm text-muted-foreground hover:text-foreground"
      >
        ← Voltar ao painel
      </Link>

      <header className="animate-rise">
        <div className="flex items-center gap-2">
          <CalendarClock className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
          <p className="text-sm font-medium text-[--zello-emerald]">Agenda</p>
        </div>
        <h1 className="mt-1 font-display text-3xl font-semibold tracking-tight">
          Agenda completa
        </h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Navegue pelos meses e toque num dia para ver os compromissos.
        </p>
      </header>

      <div className="animate-rise" style={{ animationDelay: "60ms" }}>
        <AgendaCalendar />
      </div>
    </div>
  );
}
