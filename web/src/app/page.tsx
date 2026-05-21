import Link from "next/link";
import {
  Pill,
  MessageCircleHeart,
  ShieldCheck,
  CheckCheck,
  EyeOff,
  Lock,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { SiteHeader } from "@/components/site/SiteHeader";
import { SiteFooter } from "@/components/site/SiteFooter";

export default function LandingPage() {
  return (
    <div className="flex min-h-screen flex-col">
      <SiteHeader />

      <main className="flex-1">
        <Hero />
        <Pillars />
        <HowItWorks />
        <Privacy />
        <FinalCta />
      </main>

      <SiteFooter />
    </div>
  );
}

/* ------------------------------------------------------------------ Hero */

function Hero() {
  return (
    <section className="relative overflow-hidden">
      {/* Formas organicas de fundo */}
      <div aria-hidden className="pointer-events-none absolute inset-0 -z-10">
        <div className="absolute -left-24 -top-24 h-[28rem] w-[28rem] rounded-full bg-[--zello-emerald]/15 blur-3xl animate-float-slow" />
        <div className="absolute -right-20 top-24 h-80 w-80 rounded-full bg-[--zello-amber]/25 blur-3xl animate-float-slow [animation-delay:1.5s]" />
        <div className="absolute bottom-0 left-1/3 h-72 w-72 rounded-full bg-[--zello-emerald]/10 blur-3xl" />
        <div className="absolute inset-0 bg-noise opacity-[0.5] mix-blend-multiply" />
      </div>

      <div className="container grid items-center gap-12 py-16 md:py-24 lg:grid-cols-[1.05fr_0.95fr] lg:gap-8">
        <div className="max-w-xl">
          <span className="animate-rise inline-flex items-center gap-2 rounded-full border border-border bg-card/70 px-3 py-1 text-sm font-medium text-secondary-foreground shadow-sm [animation-delay:0ms]">
            <span className="h-2 w-2 rounded-full bg-[--zello-emerald]" />
            Cuidado pelo WhatsApp, sem app pra instalar
          </span>

          <h1 className="animate-rise mt-6 text-balance text-4xl font-semibold leading-[1.05] tracking-tight text-foreground sm:text-5xl lg:text-6xl [animation-delay:80ms]">
            Alguem de olho em quem voce ama.
          </h1>

          <p className="animate-rise mt-6 text-balance text-lg leading-relaxed text-muted-foreground [animation-delay:160ms]">
            O Zello e um assistente carinhoso no WhatsApp do seu familiar idoso.
            Cuida da agenda, lembra dos remedios na hora certa, faz companhia
            todos os dias — e avisa a familia quando algo precisa de atencao.
          </p>

          <div className="animate-rise mt-8 flex flex-col gap-3 sm:flex-row [animation-delay:240ms]">
            <Button asChild size="lg" className="text-base">
              <Link href="/signup">Criar conta</Link>
            </Button>
            <Button asChild size="lg" variant="outline" className="text-base">
              <Link href="/login">Ja tenho conta</Link>
            </Button>
          </div>

          <p className="animate-rise mt-5 text-sm text-muted-foreground [animation-delay:320ms]">
            Configurou em minutos. A pessoa cuidada so precisa saber usar o
            WhatsApp.
          </p>
        </div>

        <div className="animate-rise relative [animation-delay:200ms]">
          <ChatMock />
        </div>
      </div>
    </section>
  );
}

function ChatMock() {
  return (
    <div className="relative mx-auto max-w-sm">
      {/* leve rotacao/profundidade */}
      <div
        aria-hidden
        className="absolute -inset-3 -z-10 rounded-[2rem] bg-[--zello-emerald]/10 blur-xl"
      />
      <div className="overflow-hidden rounded-[1.75rem] border border-border bg-card shadow-warm-lg">
        {/* topo estilo WhatsApp */}
        <div className="flex items-center gap-3 bg-[--zello-emerald-deep] px-4 py-3 text-[--zello-cream]">
          <div className="flex h-9 w-9 items-center justify-center rounded-full bg-[--zello-cream]/15 font-display text-sm font-semibold">
            Z
          </div>
          <div className="leading-tight">
            <p className="font-medium">Zello</p>
            <p className="text-xs text-[--zello-cream]/70">online agora</p>
          </div>
        </div>

        {/* mensagens */}
        <div className="space-y-3 bg-secondary/40 px-4 py-5">
          <BotBubble time="08:00">
            Bom dia, Dona Cida! Hora do <strong>Losartana</strong>. Ja tomou com
            um copo d&apos;agua? 💊
          </BotBubble>
          <UserBubble time="08:03">Tomei sim, querido. Obrigada!</UserBubble>
          <BotBubble time="08:03">
            Que otimo! Anotei aqui. Hoje tem a consulta da tarde, lembra? Te
            aviso 1h antes. 😊
          </BotBubble>
          <UserBubble time="08:04">Ah, ainda bem que voce lembra ❤️</UserBubble>
          <SystemNote>
            <ShieldCheck className="h-3.5 w-3.5 text-[--zello-emerald]" />
            Familia recebeu: &ldquo;Tudo bem hoje — remedio tomado, bom
            humor.&rdquo;
          </SystemNote>
        </div>
      </div>
    </div>
  );
}

function BotBubble({
  children,
  time,
}: {
  children: React.ReactNode;
  time: string;
}) {
  return (
    <div className="flex justify-start">
      <div className="max-w-[85%] rounded-2xl rounded-tl-md bg-card px-3.5 py-2.5 text-sm leading-relaxed text-card-foreground shadow-sm">
        <p>{children}</p>
        <span className="mt-1 block text-right text-[10px] text-muted-foreground">
          {time}
        </span>
      </div>
    </div>
  );
}

function UserBubble({
  children,
  time,
}: {
  children: React.ReactNode;
  time: string;
}) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[85%] rounded-2xl rounded-tr-md bg-[--zello-emerald] px-3.5 py-2.5 text-sm leading-relaxed text-[--zello-cream] shadow-sm">
        <p>{children}</p>
        <span className="mt-1 flex items-center justify-end gap-1 text-[10px] text-[--zello-cream]/70">
          {time}
          <CheckCheck className="h-3 w-3" />
        </span>
      </div>
    </div>
  );
}

function SystemNote({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex justify-center pt-1">
      <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-[--zello-cream] px-3 py-1.5 text-center text-xs text-muted-foreground">
        {children}
      </span>
    </div>
  );
}

/* --------------------------------------------------------------- Pilares */

function Pillars() {
  const pillars = [
    {
      icon: Pill,
      title: "Remedios na hora certa",
      body: "Lembra com carinho e insiste com gentileza ate confirmar. Se o horario passar sem resposta, avisa a familia — ninguem fica sem cobertura.",
    },
    {
      icon: MessageCircleHeart,
      title: "Companhia de verdade",
      body: "Uma conversa acolhedora todo dia. O Zello lembra do que importa pra pessoa — o neto, a novela, a horta — e puxa assunto de verdade.",
    },
    {
      icon: ShieldCheck,
      title: "A familia tranquila",
      body: "Voce recebe um retrato do bem-estar: humor, energia, autocuidado. Sinais agregados, nunca as mensagens literais. Privacidade respeitada.",
    },
  ];

  return (
    <section className="container py-16 md:py-24">
      <div className="mx-auto max-w-2xl text-center">
        <h2 className="text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
          Tres formas de cuidar, todos os dias
        </h2>
        <p className="mt-4 text-balance text-muted-foreground">
          O Zello acompanha de perto sem ser invasivo — pra pessoa cuidada e pra
          quem cuida.
        </p>
      </div>

      <div className="mt-12 grid gap-6 md:grid-cols-3">
        {pillars.map((p) => (
          <article
            key={p.title}
            className="group rounded-[1.25rem] border border-border bg-card p-7 shadow-warm transition-transform duration-300 hover:-translate-y-1"
          >
            <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-[--zello-emerald]/10 text-[--zello-emerald]">
              <p.icon className="h-6 w-6" />
            </div>
            <h3 className="mt-5 text-xl font-semibold tracking-tight">
              {p.title}
            </h3>
            <p className="mt-3 text-sm leading-relaxed text-muted-foreground">
              {p.body}
            </p>
          </article>
        ))}
      </div>
    </section>
  );
}

/* ----------------------------------------------------------- Como funciona */

function HowItWorks() {
  const steps = [
    {
      title: "Voce cria a conta",
      body: "Cadastra seu WhatsApp e o da pessoa que vai receber o cuidado.",
    },
    {
      title: "Chega o link no WhatsApp",
      body: "A pessoa cuidada recebe a primeira mensagem do Zello. Nada pra baixar, nada pra configurar.",
    },
    {
      title: "Conversa em portugues normal",
      body: "&ldquo;Marca a consulta de sexta&rdquo;, &ldquo;lembra do remedio das 8&rdquo;, &ldquo;como foi seu dia?&rdquo;. So conversar.",
    },
  ];

  return (
    <section className="border-y border-border/70 bg-secondary/30">
      <div className="container py-16 md:py-24">
        <div className="mx-auto max-w-2xl text-center">
          <h2 className="text-3xl font-semibold tracking-tight sm:text-4xl">
            Como funciona
          </h2>
          <p className="mt-4 text-muted-foreground">
            Simples de comecar, natural de usar.
          </p>
        </div>

        <ol className="mx-auto mt-12 grid max-w-4xl gap-6 md:grid-cols-3">
          {steps.map((s, i) => (
            <li
              key={s.title}
              className="relative rounded-[1.25rem] border border-border bg-card p-7 shadow-warm"
            >
              <span className="flex h-10 w-10 items-center justify-center rounded-full bg-[--zello-emerald] font-display text-base font-semibold text-[--zello-cream]">
                {i + 1}
              </span>
              <h3 className="mt-5 text-lg font-semibold tracking-tight">
                {s.title}
              </h3>
              <p
                className="mt-2 text-sm leading-relaxed text-muted-foreground"
                dangerouslySetInnerHTML={{ __html: s.body }}
              />
            </li>
          ))}
        </ol>
      </div>
    </section>
  );
}

/* ------------------------------------------------------------ Privacidade */

function Privacy() {
  const pillars = [
    {
      icon: EyeOff,
      title: "Sinais, nunca conversas",
      body: "A familia acompanha o bem-estar — humor, remedios, atividade. Mas nunca le o que foi dito. Nem uma linha.",
    },
    {
      icon: Lock,
      title: "O que e do idoso, fica com ele",
      body: "As conversas pertencem a quem as teve. O Zello so compartilha o que ajuda a cuidar — o resto permanece privado.",
    },
    {
      icon: ShieldCheck,
      title: "Em conformidade com a LGPD",
      body: "Dados criptografados e tratados conforme a lei. E o direito de apagar tudo, a qualquer momento.",
    },
  ];

  return (
    <section className="container py-16 md:py-24">
      <div className="relative overflow-hidden rounded-[2rem] border border-[--zello-emerald]/15 bg-[--zello-emerald]/[0.06] px-6 py-12 shadow-warm md:px-12 md:py-16">
        <div aria-hidden className="pointer-events-none absolute inset-0">
          <div className="absolute -right-20 -top-24 h-72 w-72 rounded-full bg-[--zello-emerald]/10 blur-3xl" />
          <div className="absolute inset-0 bg-noise opacity-30 mix-blend-multiply" />
        </div>

        <div className="relative mx-auto max-w-2xl text-center">
          <span className="inline-flex items-center gap-2 rounded-full border border-[--zello-emerald]/20 bg-[--zello-cream] px-3 py-1 text-xs font-semibold uppercase tracking-wide text-[--zello-emerald]">
            <ShieldCheck className="h-3.5 w-3.5" />
            Privacidade
          </span>
          <h2 className="mt-4 text-balance text-3xl font-semibold tracking-tight sm:text-4xl">
            Cuidado nao e vigilancia
          </h2>
          <p className="mt-3 text-balance text-lg leading-relaxed text-muted-foreground">
            Acompanhar quem amamos exige confianca. Por isso o Zello mostra a
            voce o que importa — sem nunca expor o que foi dito.
          </p>
        </div>

        <div className="relative mx-auto mt-10 grid max-w-5xl gap-5 md:grid-cols-3">
          {pillars.map(({ icon: Icon, title, body }) => (
            <div
              key={title}
              className="rounded-[1.5rem] border border-border bg-card p-6 shadow-warm transition-transform duration-200 hover:-translate-y-1"
            >
              <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-[--zello-emerald]/10 text-[--zello-emerald]">
                <Icon className="h-6 w-6" />
              </div>
              <h3 className="mt-4 text-lg font-semibold tracking-tight">
                {title}
              </h3>
              <p className="mt-2 leading-relaxed text-muted-foreground">
                {body}
              </p>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

/* --------------------------------------------------------------- CTA final */

function FinalCta() {
  return (
    <section className="container pb-20">
      <div className="relative overflow-hidden rounded-[2rem] bg-[--zello-emerald-deep] px-8 py-14 text-center md:px-12 md:py-20">
        <div aria-hidden className="pointer-events-none absolute inset-0">
          <div className="absolute -right-16 -top-16 h-64 w-64 rounded-full bg-[--zello-amber]/20 blur-3xl" />
          <div className="absolute -bottom-20 -left-10 h-72 w-72 rounded-full bg-[--zello-emerald]/40 blur-3xl" />
          <div className="absolute inset-0 bg-noise opacity-30 mix-blend-soft-light" />
        </div>
        <div className="relative mx-auto max-w-2xl">
          <h2 className="text-balance text-3xl font-semibold tracking-tight text-[--zello-cream] sm:text-4xl">
            Comece a cuidar melhor hoje.
          </h2>
          <p className="mt-4 text-balance text-lg text-white">
            Crie sua conta gratuitamente e deixe o Zello acompanhar quem voce
            ama, com carinho e tranquilidade.
          </p>
          <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
            <Button
              asChild
              size="lg"
              className="bg-[--zello-amber] text-[--zello-ink] text-base hover:bg-[--zello-amber]/90"
            >
              <Link href="/signup">Criar conta gratis</Link>
            </Button>
            <Button
              asChild
              size="lg"
              variant="outline"
              className="border-[--zello-cream]/30 bg-transparent text-base text-[--zello-cream] hover:bg-[--zello-cream]/10 hover:text-[--zello-cream]"
            >
              <Link href="/login">Ja tenho conta</Link>
            </Button>
          </div>
        </div>
      </div>
    </section>
  );
}
