import Link from "next/link";
import {
  CalendarPlus,
  Clock3,
  History,
  MessageCircle,
  Pill,
} from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { getMyActivity } from "@/lib/api/me";
import { getSessionCookieHeader } from "@/lib/server-cookie";
import { formatRelativeTime } from "@/lib/format";
import type { ActivityItem } from "@/types/api";

export const dynamic = "force-dynamic";

export default async function AtividadePage() {
  const cookieHeader = getSessionCookieHeader();
  const { items } = await getMyActivity(cookieHeader, 100);
  const list = items ?? [];

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
          <History className="h-5 w-5 text-[--zello-amber]" aria-hidden />
          <p className="text-sm font-medium text-[--zello-emerald]">
            Atividade
          </p>
        </div>
        <h1 className="mt-1 font-display text-3xl font-semibold tracking-tight">
          Histórico de atividade
        </h1>
        <p className="mt-2 text-sm text-muted-foreground">
          Tudo o que o Zello fez por você, do mais recente ao mais antigo.
        </p>
      </header>

      <Card className="shadow-warm animate-rise" style={{ animationDelay: "60ms" }}>
        <CardHeader>
          <CardTitle className="text-lg">Linha do tempo</CardTitle>
          <CardDescription>
            Compromissos, lembretes e conversas registrados.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {list.length === 0 ? (
            <div className="flex flex-col items-center gap-2 py-8 text-center">
              <div className="flex h-12 w-12 items-center justify-center rounded-full bg-muted text-muted-foreground">
                <Clock3 className="h-6 w-6" aria-hidden />
              </div>
              <p className="font-medium text-foreground">
                Sem atividade ainda
              </p>
              <p className="max-w-xs text-sm text-muted-foreground">
                Quando você conversar com o Zello no WhatsApp, o histórico
                aparece aqui.
              </p>
            </div>
          ) : (
            <ul className="divide-y divide-border/70">
              {list.map((item, i) => (
                <ActivityRow key={`${item.action}-${item.at}-${i}`} item={item} />
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function ActivityRow({ item }: { item: ActivityItem }) {
  return (
    <li className="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
      <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-[--zello-amber]/15 text-[--zello-amber]">
        {activityIcon(item.action)}
      </div>
      <div className="min-w-0 flex-1">
        <p className="text-sm text-foreground">{item.label}</p>
        <p className="text-xs text-muted-foreground">
          {formatRelativeTime(item.at)}
        </p>
      </div>
    </li>
  );
}

function activityIcon(action: string) {
  const cls = "h-4 w-4";
  if (action.includes("event") || action.includes("evento")) {
    return <CalendarPlus className={cls} aria-hidden />;
  }
  if (action.includes("medic") || action.includes("remed")) {
    return <Pill className={cls} aria-hidden />;
  }
  if (action.includes("message") || action.includes("conversa")) {
    return <MessageCircle className={cls} aria-hidden />;
  }
  return <Clock3 className={cls} aria-hidden />;
}
