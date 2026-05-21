import Link from "next/link";
import {
  BookHeart,
  CalendarClock,
  CalendarPlus,
  CheckCircle2,
  Clock3,
  HeartHandshake,
  HeartPulse,
  MapPin,
  MessageCircle,
  Pill,
  Plane,
  Sparkles,
  TrendingUp,
  UserPlus,
  Users,
  Zap,
} from "lucide-react";

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
import { getMyAgenda, getMyInsights, getProfileFacts } from "@/lib/api/me";
import { listDependents } from "@/lib/api/family";
import { getSessionCookieHeader } from "@/lib/server-cookie";
import {
  formatEventWhen,
  formatRelativeTime,
  formatTripPeriod,
  greetingForHour,
} from "@/lib/format";
import type {
  ActivityItem,
  AgendaEvent,
  AgendaResponse,
  DependentEntry,
  Insight,
  InsightKind,
  InsightsResponse,
  PersonFact,
  ProfileFacts,
  RelationFact,
  TripFact,
  User,
} from "@/types/api";

export const dynamic = "force-dynamic";

const EMPTY_AGENDA: AgendaResponse = {
  google_connected: false,
  upcoming: [],
  recent_activity: [],
};

const EMPTY_FACTS: ProfileFacts = {
  available: false,
  relations: [],
  people: [],
  trips: [],
};

export default async function DashboardHome() {
  const cookieHeader = getSessionCookieHeader();

  // getMe ja foi validado no layout; aqui falha real borbulha (auth quebrada).
  const me = await getMe(cookieHeader);

  // Cada chamada e independente — falha de uma nao derruba a pagina.
  const [agenda, insights, facts, dependents] = await Promise.all([
    safe(() => getMyAgenda(cookieHeader), EMPTY_AGENDA),
    safe(
      () => getMyInsights(cookieHeader, 30),
      emptyInsights(),
    ),
    safe(() => getProfileFacts(cookieHeader), EMPTY_FACTS),
    safeDependents(() => listDependents(cookieHeader)),
  ]);

  // Normaliza arrays nil (vide nota acima sobre slice nil do Go).
  facts.relations = facts.relations ?? [];
  facts.people = facts.people ?? [];
  facts.trips = facts.trips ?? [];

  // Backend Go pode serializar slice nil como `null` no JSON (resposta de
  // sucesso, fora do alcance do safe()). Normaliza pra [] antes de renderizar.
  agenda.upcoming = agenda.upcoming ?? [];
  agenda.recent_activity = agenda.recent_activity ?? [];
  insights.insights = insights.insights ?? [];

  const contextLine = buildContextLine(agenda, dependents.length);

  return (
    <div className="space-y-10">
      <section className="animate-rise">
        <p className="text-sm font-medium text-[--zello-emerald]">
          {greetingForHour()}
        </p>
        <h1 className="mt-1 font-display text-4xl font-semibold tracking-tight text-foreground sm:text-5xl">
          Olá, {firstName(me)}.
        </h1>
        <p className="mt-3 max-w-prose text-base text-muted-foreground">
          {contextLine}
        </p>
      </section>

      <AgendaSection agenda={agenda} />

      <InsightsSection insights={insights} />

      <ProfileFactsSection facts={facts} />

      <FamilySection dependents={dependents} />

      <FooterLinks />
    </div>
  );
}

/* ---------------------------------------------------------------- agenda */

function AgendaSection({ agenda }: { agenda: AgendaResponse }) {
  return (
    <section
      className="space-y-4 animate-rise"
      style={{ animationDelay: "60ms" }}
      aria-labelledby="agenda-title"
    >
      <header className="flex items-center gap-2">
        <CalendarClock className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
        <h2
          id="agenda-title"
          className="font-display text-2xl font-semibold tracking-tight"
        >
          Minha agenda
        </h2>
      </header>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <UpcomingCard agenda={agenda} />
        <ActivityCard items={agenda.recent_activity} />
      </div>
    </section>
  );
}

function UpcomingCard({ agenda }: { agenda: AgendaResponse }) {
  return (
    <Card className="shadow-warm">
      <CardHeader>
        <CardTitle className="text-lg">Próximos compromissos</CardTitle>
        <CardDescription>O que vem pela frente na sua agenda.</CardDescription>
      </CardHeader>
      <CardContent>
        {!agenda.google_connected ? (
          <EmptyHint
            icon={<CalendarClock className="h-6 w-6" aria-hidden />}
            title="Agenda ainda não conectada"
            body="Conecte seu Google Calendar pelo WhatsApp para ver seus compromissos aqui."
          />
        ) : agenda.upcoming.length === 0 ? (
          <EmptyHint
            icon={<CheckCircle2 className="h-6 w-6" aria-hidden />}
            title="Tudo tranquilo"
            body="Nada nos próximos dias."
          />
        ) : (
          <ul className="divide-y divide-border/70">
            {agenda.upcoming.map((ev) => (
              <EventRow key={ev.id} event={ev} />
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}

function EventRow({ event }: { event: AgendaEvent }) {
  return (
    <li className="flex gap-3 py-3 first:pt-0 last:pb-0">
      <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-[--zello-emerald]/10 text-[--zello-emerald]">
        <CalendarClock className="h-4 w-4" aria-hidden />
      </div>
      <div className="min-w-0">
        <p className="truncate font-medium text-foreground">{event.title}</p>
        <p className="text-sm text-muted-foreground">
          {formatEventWhen(event.start, event.all_day)}
        </p>
        {event.location ? (
          <p className="mt-0.5 flex items-center gap-1 text-sm text-muted-foreground">
            <MapPin className="h-3.5 w-3.5 shrink-0" aria-hidden />
            <span className="truncate">{event.location}</span>
          </p>
        ) : null}
      </div>
    </li>
  );
}

function ActivityCard({ items }: { items: ActivityItem[] }) {
  return (
    <Card className="shadow-warm">
      <CardHeader>
        <CardTitle className="text-lg">Atividade recente</CardTitle>
        <CardDescription>O que o Zello fez por você ultimamente.</CardDescription>
      </CardHeader>
      <CardContent>
        {items.length === 0 ? (
          <EmptyHint
            icon={<Clock3 className="h-6 w-6" aria-hidden />}
            title="Sem atividade recente"
            body="Quando você conversar com o Zello no WhatsApp, o histórico aparece aqui."
          />
        ) : (
          <>
            <ul className="divide-y divide-border/70">
              {items.map((item, i) => (
                <ActivityRow
                  key={`${item.action}-${item.at}-${i}`}
                  item={item}
                />
              ))}
            </ul>
            <div className="mt-4 border-t border-border/70 pt-3">
              <Link
                href="/dashboard/atividade"
                className="text-sm font-medium text-[--zello-emerald] underline-offset-4 hover:underline"
              >
                Ver histórico completo →
              </Link>
            </div>
          </>
        )}
      </CardContent>
    </Card>
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

/* -------------------------------------------------------------- insights */

function InsightsSection({ insights }: { insights: InsightsResponse }) {
  return (
    <section
      className="space-y-4 animate-rise"
      style={{ animationDelay: "120ms" }}
      aria-labelledby="insights-title"
    >
      <header className="flex flex-wrap items-center gap-2">
        <Sparkles className="h-5 w-5 text-[--zello-amber]" aria-hidden />
        <h2
          id="insights-title"
          className="font-display text-2xl font-semibold tracking-tight"
        >
          Insights
        </h2>
        <span className="inline-flex items-center gap-1 rounded-full bg-[--zello-amber]/15 px-2.5 py-0.5 text-xs font-medium text-[--zello-emerald-deep]">
          <Sparkles className="h-3 w-3" aria-hidden />
          gerado por IA
        </span>
      </header>

      {insights.available ? (
        <div className="space-y-5">
          {insights.summary ? (
            <Card className="overflow-hidden border-[--zello-emerald]/20 bg-[--zello-emerald]/5 shadow-warm">
              <CardContent className="p-6 sm:p-8">
                <p className="font-display text-2xl font-medium leading-snug tracking-tight text-[--zello-emerald-deep] sm:text-3xl">
                  {insights.summary}
                </p>
                <p className="mt-3 text-xs text-muted-foreground">
                  Análise dos últimos {insights.period_days} dias.
                </p>
              </CardContent>
            </Card>
          ) : null}

          {insights.insights.length > 0 ? (
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
              {insights.insights.map((ins, i) => (
                <InsightCard key={`${ins.kind}-${i}`} insight={ins} />
              ))}
            </div>
          ) : null}
        </div>
      ) : (
        <Card className="border-dashed bg-muted/30 shadow-warm">
          <CardContent className="flex flex-col items-center gap-3 p-8 text-center sm:p-10">
            <div className="flex h-12 w-12 items-center justify-center rounded-full bg-[--zello-amber]/15 text-[--zello-amber]">
              <Sparkles className="h-6 w-6" aria-hidden />
            </div>
            <p className="max-w-md text-base text-muted-foreground">
              O Zello ainda está aprendendo seu padrão de uso — volte em alguns
              dias para ver seus primeiros insights.
            </p>
          </CardContent>
        </Card>
      )}
    </section>
  );
}

function InsightCard({ insight }: { insight: Insight }) {
  return (
    <Card className="h-full shadow-warm transition-shadow hover:shadow-warm-lg">
      <CardHeader className="space-y-3">
        <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-[--zello-emerald]/10 text-[--zello-emerald]">
          {insightIcon(insight.kind)}
        </div>
        <CardTitle className="text-base leading-snug">{insight.title}</CardTitle>
      </CardHeader>
      <CardContent>
        <p className="text-sm leading-relaxed text-muted-foreground">
          {insight.detail}
        </p>
      </CardContent>
    </Card>
  );
}

function insightIcon(kind: InsightKind) {
  const cls = "h-5 w-5";
  switch (kind) {
    case "pattern":
      return <TrendingUp className={cls} aria-hidden />;
    case "health":
      return <HeartPulse className={cls} aria-hidden />;
    case "social":
      return <Users className={cls} aria-hidden />;
    case "productivity":
      return <Zap className={cls} aria-hidden />;
    default:
      return <Sparkles className={cls} aria-hidden />;
  }
}

/* ------------------------------------------------- o que o Zello sabe */

function ProfileFactsSection({ facts }: { facts: ProfileFacts }) {
  const relations = facts.relations ?? [];
  const people = facts.people ?? [];
  const trips = facts.trips ?? [];
  const hasPeople = relations.length > 0 || people.length > 0;
  const hasTrips = trips.length > 0;
  const hasAnything = facts.available && (hasPeople || hasTrips);

  return (
    <section
      className="space-y-4 animate-rise"
      style={{ animationDelay: "150ms" }}
      aria-labelledby="facts-title"
    >
      <header className="flex items-center gap-2">
        <BookHeart className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
        <h2
          id="facts-title"
          className="font-display text-2xl font-semibold tracking-tight"
        >
          O que o Zello sabe sobre você
        </h2>
      </header>

      {!hasAnything ? (
        <Card className="border-dashed bg-muted/30 shadow-warm">
          <CardContent className="flex flex-col items-center gap-3 p-8 text-center">
            <div className="flex h-12 w-12 items-center justify-center rounded-full bg-[--zello-emerald]/10 text-[--zello-emerald]">
              <BookHeart className="h-6 w-6" aria-hidden />
            </div>
            <p className="max-w-md text-base text-muted-foreground">
              O Zello vai aprendendo sobre você conforme conversam — pessoas,
              viagens e rotinas aparecem aqui.
            </p>
          </CardContent>
        </Card>
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
          {hasPeople ? (
            <PeopleCard relations={relations} people={people} />
          ) : null}
          {hasTrips ? <TripsCard trips={trips} /> : null}
        </div>
      )}
    </section>
  );
}

function PeopleCard({
  relations,
  people,
}: {
  relations: RelationFact[];
  people: PersonFact[];
}) {
  return (
    <Card className="shadow-warm">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-lg">
          <Users className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
          Pessoas na sua vida
        </CardTitle>
        <CardDescription>
          Quem o Zello conhece a partir das suas conversas.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ul className="divide-y divide-border/70">
          {relations.map((r, i) => (
            <PersonRow
              key={`rel-${r.name}-${i}`}
              name={r.name}
              detail={r.relation}
            />
          ))}
          {people.map((p, i) => (
            <PersonRow
              key={`per-${p.name}-${i}`}
              name={p.name}
              detail={p.detail}
            />
          ))}
        </ul>
      </CardContent>
    </Card>
  );
}

function PersonRow({ name, detail }: { name: string; detail: string }) {
  return (
    <li className="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
      <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-[--zello-emerald]/10 text-[--zello-emerald]">
        <HeartHandshake className="h-4 w-4" aria-hidden />
      </div>
      <div className="min-w-0 flex-1">
        <p className="truncate font-medium text-foreground">{name}</p>
        {detail ? (
          <p className="text-sm capitalize text-muted-foreground">{detail}</p>
        ) : null}
      </div>
    </li>
  );
}

function TripsCard({ trips }: { trips: TripFact[] }) {
  return (
    <Card className="shadow-warm">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-lg">
          <Plane className="h-5 w-5 text-[--zello-amber]" aria-hidden />
          Viagens
        </CardTitle>
        <CardDescription>Destinos e períodos que o Zello sabe.</CardDescription>
      </CardHeader>
      <CardContent>
        <ul className="divide-y divide-border/70">
          {trips.map((t, i) => (
            <li
              key={`${t.destination}-${i}`}
              className="flex items-start gap-3 py-3 first:pt-0 last:pb-0"
            >
              <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-[--zello-amber]/15 text-[--zello-amber]">
                <MapPin className="h-4 w-4" aria-hidden />
              </div>
              <div className="min-w-0 flex-1">
                <p className="truncate font-medium text-foreground">
                  {t.destination || t.label}
                </p>
                <p className="text-sm text-muted-foreground">
                  {formatTripPeriod(t.start, t.end) || t.label}
                </p>
              </div>
            </li>
          ))}
        </ul>
      </CardContent>
    </Card>
  );
}

/* ---------------------------------------------------------------- family */

function FamilySection({ dependents }: { dependents: DependentEntry[] }) {
  const hasDependents = dependents.length > 0;

  return (
    <section
      className="space-y-4 animate-rise"
      style={{ animationDelay: "180ms" }}
      aria-labelledby="family-title"
    >
      <header className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <HeartHandshake
            className="h-5 w-5 text-[--zello-emerald]"
            aria-hidden
          />
          <h2
            id="family-title"
            className="font-display text-2xl font-semibold tracking-tight"
          >
            Quem você cuida
          </h2>
        </div>
        {hasDependents ? (
          <Button asChild>
            <Link href="/dashboard/family/new">
              <UserPlus className="h-4 w-4" aria-hidden />
              Adicionar pessoa
            </Link>
          </Button>
        ) : null}
      </header>

      {hasDependents ? (
        <DependentList dependents={dependents} />
      ) : (
        <FamilyEmptyState />
      )}
    </section>
  );
}

function FamilyEmptyState() {
  return (
    <Card className="overflow-hidden border-[--zello-emerald]/20 bg-[--zello-emerald]/[0.04] shadow-warm">
      <CardContent className="flex flex-col items-center gap-5 p-8 text-center sm:p-12">
        <div className="flex h-16 w-16 items-center justify-center rounded-2xl bg-[--zello-emerald]/10 text-[--zello-emerald]">
          <HeartHandshake className="h-8 w-8" aria-hidden />
        </div>
        <div className="space-y-2">
          <h3 className="font-display text-2xl font-semibold tracking-tight text-foreground">
            Cuide de quem você ama
          </h3>
          <p className="mx-auto max-w-md text-base leading-relaxed text-muted-foreground">
            Cadastre um familiar para o Zello começar a cuidar — lembretes de
            remédio, companhia no dia a dia e um resumo de bem-estar pra você.
          </p>
        </div>
        <Button asChild size="lg">
          <Link href="/dashboard/family/new">
            <UserPlus className="h-4 w-4" aria-hidden />
            Cadastrar primeira pessoa
          </Link>
        </Button>
      </CardContent>
    </Card>
  );
}

/* ---------------------------------------------------------------- footer */

function FooterLinks() {
  return (
    <section className="border-t border-border/70 pt-6">
      <p className="text-sm text-muted-foreground">
        Ajuste lembretes, resumos e horários nas{" "}
        <Link
          href="/dashboard/preferences"
          className="font-medium text-[--zello-emerald] underline-offset-4 hover:underline"
        >
          suas preferências
        </Link>
        .
      </p>
    </section>
  );
}

/* ------------------------------------------------------------- helpers UI */

function EmptyHint({
  icon,
  title,
  body,
}: {
  icon: React.ReactNode;
  title: string;
  body: string;
}) {
  return (
    <div className="flex flex-col items-center gap-2 py-6 text-center">
      <div className="flex h-12 w-12 items-center justify-center rounded-full bg-muted text-muted-foreground">
        {icon}
      </div>
      <p className="font-medium text-foreground">{title}</p>
      <p className="max-w-xs text-sm text-muted-foreground">{body}</p>
    </div>
  );
}

/* ----------------------------------------------------------- data helpers */

function firstName(u: User): string {
  return u.name.split(" ")[0] || u.name;
}

function buildContextLine(agenda: AgendaResponse, dependentCount: number): string {
  const parts: string[] = [];
  const n = agenda.upcoming.length;
  if (agenda.google_connected && n > 0) {
    parts.push(
      n === 1
        ? "Você tem 1 compromisso à frente"
        : `Você tem ${n} compromissos à frente`,
    );
  }
  if (dependentCount > 0) {
    parts.push(
      dependentCount === 1
        ? "e está cuidando de 1 pessoa"
        : `e está cuidando de ${dependentCount} pessoas`,
    );
  }
  if (parts.length === 0) {
    return "Aqui você acompanha sua agenda, seus insights e quem você cuida.";
  }
  return `${parts.join(" ")}.`;
}

function emptyInsights(): InsightsResponse {
  return {
    generated_at: new Date().toISOString(),
    period_days: 30,
    available: false,
    summary: "",
    insights: [],
  };
}

/** Executa `fn`, devolvendo `fallback` em qualquer falha (rede/API/parse). */
async function safe<T>(fn: () => Promise<T>, fallback: T): Promise<T> {
  try {
    return await fn();
  } catch {
    return fallback;
  }
}

/**
 * Lista dependentes com fallback vazio. 403 (`not_responsavel`) e qualquer
 * outra falha viram lista vazia — o backend pode permitir cadastro mesmo a
 * quem ainda nao tem dependentes.
 */
async function safeDependents(
  fn: () => Promise<{ dependents: DependentEntry[] }>,
): Promise<DependentEntry[]> {
  try {
    const res = await fn();
    return res.dependents ?? [];
  } catch (err) {
    if (err instanceof ApiError) {
      return [];
    }
    return [];
  }
}
