import Link from "next/link";
import {
  Bell,
  Briefcase,
  Calendar,
  Check,
  Eye,
  Feather,
  Heart,
  Lock,
  MessageCircleHeart,
  Pill,
  ShieldCheck,
  type LucideIcon,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { SiteHeader } from "@/components/site/SiteHeader";
import { SiteFooter } from "@/components/site/SiteFooter";
import { ChatCarousel } from "@/components/landing/ChatCarousel";

export default function LandingPage() {
  return (
    <div className="flex min-h-screen flex-col">
      <SiteHeader />

      <main className="flex-1">
        <Hero />
        <TwoWorlds />
        <HowItWorks />
        <Privacy />
        <QuietCta />
      </main>

      <SiteFooter />
    </div>
  );
}

/* ------------------------------------------------------------------ Hero */

function Hero() {
  return (
    <section className="relative overflow-hidden">
      <div aria-hidden className="pointer-events-none absolute inset-0 -z-10">
        <div className="absolute -left-28 -top-28 h-[30rem] w-[30rem] rounded-full bg-[--zello-emerald]/15 blur-3xl animate-float-slow" />
        <div className="absolute -right-24 top-16 h-[21rem] w-[21rem] rounded-full bg-[--zello-amber]/25 blur-3xl animate-float-slow [animation-delay:1.5s]" />
        <div className="absolute -bottom-28 left-1/3 h-72 w-72 rounded-full bg-[--zello-emerald]/10 blur-3xl" />
        <div className="absolute inset-0 bg-noise opacity-[0.55] mix-blend-multiply" />
      </div>

      <div className="container grid grid-cols-1 items-center gap-10 py-12 md:gap-16 md:py-24 lg:grid-cols-2">
        <div className="max-w-[520px]">
          <h1 className="animate-rise text-balance font-display text-[40px] font-semibold leading-[1.02] tracking-[-0.025em] text-foreground md:text-6xl">
            Pra você.{" "}
            <span className="italic text-[--zello-emerald]">
              Pra quem você ama.
            </span>
          </h1>

          <p className="animate-rise mt-5 text-balance text-base leading-relaxed text-muted-foreground md:mt-6 md:text-[19px] [animation-delay:160ms]">
            O Zello é um assistente intencional no WhatsApp. Organiza sua semana,
            lembra dos remédios da sua mãe, garante que ninguém esqueça o judô da
            Lúcia. Tudo na mesma conversa que você já tem todo dia.
          </p>

          <div className="animate-rise mt-7 flex flex-wrap gap-3 md:mt-9 [animation-delay:240ms]">
            <Button asChild size="lg" className="text-base">
              <Link href="/signup">Criar conta</Link>
            </Button>
            <Button
              asChild
              size="lg"
              variant="outline"
              className="bg-card text-base"
            >
              <Link href="/login">Já tenho conta</Link>
            </Button>
          </div>

          <div className="animate-rise mt-7 flex flex-wrap gap-x-[18px] gap-y-2 text-[13px] text-muted-foreground [animation-delay:320ms]">
            <span className="inline-flex items-center gap-1.5">
              <Check className="h-3.5 w-3.5 text-[--zello-emerald]" /> Sem app
              pra instalar
            </span>
            <span className="inline-flex items-center gap-1.5">
              <Check className="h-3.5 w-3.5 text-[--zello-emerald]" /> Configurou
              em minutos
            </span>
            <span className="inline-flex items-center gap-1.5">
              <Check className="h-3.5 w-3.5 text-[--zello-emerald]" /> Funciona em
              português, normal
            </span>
          </div>
        </div>

        <div className="animate-rise mx-auto w-full max-w-[420px] lg:mx-0 lg:max-w-none [animation-delay:200ms]">
          <ChatCarousel />
        </div>
      </div>
    </section>
  );
}

/* ------------------------------------------------- Dois mundos, uma conversa */

type WorldItem = { icon: LucideIcon; title: string; body: string };
type World = {
  label: string;
  icon: LucideIcon;
  title: string;
  body: string;
  items: WorldItem[];
};

function TwoWorlds() {
  const worlds: World[] = [
    {
      label: "Pra você",
      icon: Briefcase,
      title: "Uma semana que respira.",
      body: "Conflitos antecipados, hora de buscar a filha protegida, foco no que importa. O Zello segura os detalhes pra você focar no que importa.",
      items: [
        {
          icon: Calendar,
          title: "Marca e organiza",
          body: "Encaminhe um convite e ele vira evento. Detecta conflitos antes deles existirem.",
        },
        {
          icon: Bell,
          title: "Avisa antes",
          body: "Alertas no momento que faz sentido pra você. Quantos quiser, do jeito que precisar.",
        },
        {
          icon: Feather,
          title: "Protege o que importa",
          body: "Saída pra buscar a filha, almoço com alguém, bloco de foco — fica no calendário, e ninguém pisa em cima.",
        },
      ],
    },
    {
      label: "Pra quem você ama",
      icon: Heart,
      title: "Presença, todos os dias.",
      body: "O cuidado diário, sem o desgaste diário. O Zello fica com o checklist. Você fica com o vínculo.",
      items: [
        {
          icon: Pill,
          title: "Remédios na hora certa",
          body: "Lembra com carinho e insiste com gentileza até confirmar. Se o horário passa sem resposta, a família é avisada — ninguém fica sem cobertura.",
        },
        {
          icon: MessageCircleHeart,
          title: "Companhia de verdade",
          body: "Lembra do que importa pra pessoa — o neto, a novela, a horta — e puxa assunto de verdade. Não é chat genérico, é conversa de quem presta atenção.",
        },
        {
          icon: ShieldCheck,
          title: "Família tranquila",
          body: "Você recebe um retrato do bem-estar: humor, energia, autocuidado. Sinais agregados, nunca as mensagens literais. Privacidade respeitada.",
        },
      ],
    },
  ];

  return (
    <section className="border-y border-border/70 bg-secondary/[0.45]">
      <div className="container py-16 md:py-[88px]">
        <div className="mx-auto max-w-[620px] text-center">
          <p className="text-[13px] font-semibold uppercase tracking-[0.12em] text-[--zello-emerald]">
            Dois mundos, uma conversa
          </p>
          <h2 className="mt-3 text-balance font-display text-[28px] font-semibold leading-[1.1] tracking-[-0.02em] md:text-[38px]">
            O Zello atende a vida toda, sem misturar as caixas.
          </h2>
          <p className="mt-4 text-balance text-[15px] leading-relaxed text-muted-foreground md:text-[17px]">
            Você fala com o Zello no seu WhatsApp. A sua mãe fala com o Zello no
            WhatsApp dela. Cada um na própria conversa, no próprio ritmo — e tudo
            conectado por detrás.
          </p>
        </div>

        <div className="mt-10 grid grid-cols-1 gap-5 md:mt-14 md:grid-cols-2 md:gap-7">
          {worlds.map((w) => (
            <article
              key={w.label}
              className="flex flex-col gap-[18px] rounded-3xl border border-border bg-card p-6 shadow-warm md:p-9"
            >
              <div className="flex items-center gap-3">
                <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-[--zello-emerald]/10 text-[--zello-emerald]">
                  <w.icon className="h-5 w-5" />
                </span>
                <p className="text-[13px] font-semibold uppercase tracking-[0.1em] text-muted-foreground">
                  {w.label}
                </p>
              </div>

              <h3 className="text-balance font-display text-[22px] font-semibold leading-[1.1] tracking-[-0.02em] md:text-[28px]">
                {w.title}
              </h3>
              <p className="text-[15.5px] leading-[1.65] text-muted-foreground">
                {w.body}
              </p>

              <div className="mt-2 flex flex-col border-t border-border/70 pt-2">
                {w.items.map((it, i) => (
                  <div
                    key={it.title}
                    className={`grid grid-cols-[auto_1fr] items-start gap-4 py-4 ${
                      i === 0 ? "" : "border-t border-border/70"
                    }`}
                  >
                    <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-[10px] bg-[--zello-emerald]/[0.08] text-[--zello-emerald]">
                      <it.icon className="h-[18px] w-[18px]" />
                    </span>
                    <div>
                      <p className="font-display text-[17px] font-semibold leading-tight tracking-[-0.01em] text-foreground">
                        {it.title}
                      </p>
                      <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
                        {it.body}
                      </p>
                    </div>
                  </div>
                ))}
              </div>
            </article>
          ))}
        </div>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------- Como funciona */

function HowItWorks() {
  const steps = [
    {
      n: "01",
      title: "Cria a conta",
      body: "Cadastra seu WhatsApp em segundos. Se quiser, adiciona quem você cuida — seu pai, sua mãe, seu filho — com o número deles.",
    },
    {
      n: "02",
      title: "Conecta o Google Calendar",
      body: "Num toque, o Zello passa a ver sua agenda e marcar pra você. Sem isso ele também funciona pros lembretes — mas com isso ele resolve conflitos.",
    },
    {
      n: "03",
      title: "Daí em diante, só conversa.",
      body: "Você fala como falaria com alguém que entende sua vida. Sem app pra abrir, sem dashboard pra checar. Só o WhatsApp.",
    },
  ];

  return (
    <section id="como-funciona" className="container py-16 md:py-24">
      <div className="grid grid-cols-1 items-start gap-6 md:grid-cols-[minmax(220px,280px)_1fr] md:gap-16">
        <div className="md:sticky md:top-24">
          <p className="text-[13px] font-semibold uppercase tracking-[0.12em] text-[--zello-emerald]">
            Como funciona
          </p>
          <h2 className="mt-3 text-balance font-display text-[30px] font-semibold leading-[1.05] tracking-[-0.02em] md:text-[38px]">
            Três passos. Nenhum app.
          </h2>
        </div>

        <ol className="flex flex-col">
          {steps.map((s, i) => (
            <li
              key={s.n}
              className={`grid grid-cols-[auto_1fr] items-start gap-[18px] py-5 md:gap-7 md:py-[26px] ${
                i === 0 ? "" : "border-t border-border/70"
              }`}
            >
              <span className="font-display text-[28px] font-medium italic leading-none tracking-[-0.02em] text-[--zello-amber] md:text-[38px]">
                {s.n}
              </span>
              <div>
                <h3 className="font-display text-xl font-semibold tracking-[-0.01em] md:text-2xl">
                  {s.title}
                </h3>
                <p className="mt-1.5 max-w-[520px] text-[15.5px] leading-[1.65] text-muted-foreground">
                  {s.body}
                </p>
              </div>
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}

/* ------------------------------------------------------------ Privacidade */

function Privacy() {
  const points: WorldItem[] = [
    {
      icon: Eye,
      title: "Você vê sinais, não conversas.",
      body: "A família recebe um retrato de bem-estar — humor, energia, autocuidado. Nunca o que foi dito.",
    },
    {
      icon: Lock,
      title: "Nada sai daqui sem propósito.",
      body: "Conformidade com a LGPD, criptografia em repouso, e cada notificação tem um motivo claro.",
    },
    {
      icon: Feather,
      title: "Cuidar não é vigiar.",
      body: "O Zello insiste com gentileza, não com pressão. Se a pessoa quiser silêncio, ela tem silêncio.",
    },
  ];

  return (
    <section id="privacidade" className="container py-16 md:py-24">
      <div className="grid grid-cols-1 items-start gap-8 md:grid-cols-[minmax(280px,360px)_1fr] md:gap-16">
        <div className="md:sticky md:top-24">
          <p className="text-[13px] font-semibold uppercase tracking-[0.12em] text-[--zello-emerald]">
            Privacidade
          </p>
          <h2 className="mt-3 text-balance font-display text-[32px] font-semibold leading-[1.05] tracking-[-0.02em] md:text-[42px]">
            Atenção é um tipo de{" "}
            <span className="italic text-[--zello-emerald]">cuidado</span>.<br />
            Vigilância é outro.
          </h2>
          <p className="mt-5 max-w-[320px] text-balance text-base leading-[1.65] text-muted-foreground">
            O Zello foi desenhado pra notar o que importa — sem nunca expor o que
            foi dito. A confiança da pessoa cuidada é o ativo mais frágil do
            produto, e o tratamos como tal.
          </p>
        </div>

        <ul className="flex flex-col gap-[22px]">
          {points.map(({ icon: Icon, title, body }) => (
            <li
              key={title}
              className="grid grid-cols-[40px_1fr] items-start gap-[18px]"
            >
              <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-[--zello-emerald]/[0.08] text-[--zello-emerald]">
                <Icon className="h-5 w-5" />
              </span>
              <div>
                <p className="font-display text-lg font-semibold tracking-[-0.01em] text-foreground md:text-xl">
                  {title}
                </p>
                <p className="mt-1.5 max-w-[520px] text-[15px] leading-relaxed text-muted-foreground">
                  {body}
                </p>
              </div>
            </li>
          ))}
        </ul>
      </div>
    </section>
  );
}

/* ----------------------------------------------------------- Fechamento */

function QuietCta() {
  return (
    <section className="container pb-16 md:pb-24">
      <div className="mx-auto grid max-w-[980px] grid-cols-1 items-center gap-6 border-t border-border/70 pt-10 md:grid-cols-[1fr_auto] md:gap-10 md:pt-16">
        <div>
          <h3 className="text-balance font-display text-[26px] font-semibold leading-[1.1] tracking-[-0.02em] md:text-[30px]">
            Comece com uma conversa.
          </h3>
          <p className="mt-2 max-w-[520px] text-balance text-base text-muted-foreground">
            Você cadastra seu número, escolhe quem mais o Zello vai acompanhar, e
            o resto continua no WhatsApp — como sempre foi.
          </p>
        </div>
        <div className="flex flex-wrap gap-3">
          <Button asChild size="lg" className="text-base">
            <Link href="/signup">Criar conta</Link>
          </Button>
          <Button
            asChild
            size="lg"
            variant="outline"
            className="bg-card text-base"
          >
            <Link href="/login">Já tenho conta</Link>
          </Button>
        </div>
      </div>
    </section>
  );
}
