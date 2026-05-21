"use client";

import * as React from "react";
import { ArrowLeft, ArrowRight, CheckCheck, ShieldCheck } from "lucide-react";

type Bubble =
  | { who: "zello" | "me"; time: string; text?: string; html?: string }
  | { who: "system"; text: string };

type Scenario = {
  id: string;
  audience: string;
  persona: string;
  personaDetail: string;
  caption: string;
  bubbles: Bubble[];
};

const SCENARIOS: Scenario[] = [
  {
    id: "familia",
    audience: "Pra quem você ama",
    persona: "Dona Cida",
    personaDetail: "85 anos · sua mãe",
    caption: "A medicação da manhã, sem você precisar lembrar.",
    bubbles: [
      {
        who: "zello",
        time: "08:00",
        html: "Bom dia, Dona Cida! Hora do <b>Losartana</b>. Já tomou com um copo d’água? 💊",
      },
      { who: "me", time: "08:03", text: "Tomei sim, querido. Obrigada!" },
      {
        who: "zello",
        time: "08:03",
        text: "Que ótimo! Hoje tem a consulta com o Dr. Fernando 10h, lembra? Te aviso novamente 1h antes. 😊",
      },
      { who: "me", time: "08:04", text: "Ah, ainda bem que você lembrou, vou me arrumar aqui ❤️" },
      {
        who: "zello",
        time: "08:05",
        text: "Estou à disposição!",
      }, {
        who: "system",
        text: "Família avisada: “tudo bem hoje — remédio tomado, bom humor.”",
      },
    ],
  },
  {
    id: "agenda",
    audience: "Pra você",
    persona: "Você",
    personaDetail: "trabalho · vida · tudo no meio",
    caption: "A semana inteira numa conversa.",
    bubbles: [
      {
        who: "me",
        time: "09:12",
        html: '<i style="opacity:.7">↪ Convite encaminhado: “Workshop Bruno + time” — qui 14h, 2h</i>',
      },
      { who: "me", time: "09:12", text: "Pode marcar?" },
      {
        who: "zello",
        time: "09:12",
        text: "Agora mesmo! Só uma coisa, você tem dentista quinta às 15h. Devo reagendar o dentista pra sexta de manhã, ou você prefere mover o workshop pra outro horário?",
      },
      { who: "me", time: "09:13", text: "Reagenda o dentista pra sexta 9h." },
      {
        who: "zello",
        time: "09:13",
        text: "Feito. Sexta 9h ok pra clínica. Workshop confirmado pra quinta 14h. Te aviso 1h antes dos dois.",
      },
      {
        who: "system",
        text: "1 compromisso marcado · 1 conflito resolvido · 0 pendências.",
      },
    ],
  },
];

export function ChatCarousel() {
  const [index, setIndex] = React.useState(0);
  const scenario = SCENARIOS[index];
  const scrollRef = React.useRef<HTMLDivElement>(null);
  const pausedRef = React.useRef(false);

  // Ao trocar de cenário: reseta e faz auto-scroll lento do topo ao fim.
  // Pausa permanentemente assim que o usuário interage (wheel/touch/hover).
  React.useEffect(() => {
    pausedRef.current = false;
    const el = scrollRef.current;
    if (!el) return;
    el.scrollTop = 0;

    let raf = 0;
    let t0: number | null = null;
    const startDelay = 1400;
    const duration = 6500;

    const tick = (now: number) => {
      if (pausedRef.current) return;
      if (t0 === null) t0 = now;
      const elapsed = now - t0;
      if (elapsed < startDelay) {
        raf = requestAnimationFrame(tick);
        return;
      }
      const max = el.scrollHeight - el.clientHeight;
      if (max <= 4) return;
      const t = Math.min(1, (elapsed - startDelay) / duration);
      const eased = t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2;
      el.scrollTop = max * eased;
      if (t < 1) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);

    const pause = () => {
      pausedRef.current = true;
    };
    el.addEventListener("wheel", pause, { passive: true });
    el.addEventListener("touchstart", pause, { passive: true });
    el.addEventListener("mouseenter", pause);

    return () => {
      cancelAnimationFrame(raf);
      el.removeEventListener("wheel", pause);
      el.removeEventListener("touchstart", pause);
      el.removeEventListener("mouseenter", pause);
    };
  }, [index]);

  return (
    <div className="relative">
      <div
        aria-hidden
        className="absolute -inset-4 -z-10 rounded-[2.5rem] bg-[--zello-emerald]/[0.08] blur-xl"
      />

      <div className="overflow-hidden rounded-[1.75rem] border border-border bg-card shadow-warm-lg">
        {/* topo estilo WhatsApp */}
        <div className="flex items-center gap-3 bg-[--zello-emerald-deep] px-4 py-3 text-[--zello-cream]">
          <div className="flex h-[34px] w-[34px] items-center justify-center rounded-full bg-[--zello-cream]/15 font-display text-sm font-semibold">
            Z
          </div>
          <div className="min-w-0 flex-1 leading-tight">
            <p className="text-sm font-medium">Zello</p>
            <p className="truncate text-[11px] text-[--zello-cream]/70">
              com {scenario.persona} · {scenario.personaDetail}
            </p>
          </div>
        </div>

        {/* area de mensagens com auto-scroll */}
        <div
          ref={scrollRef}
          key={scenario.id}
          className="chat-scroll flex h-[360px] flex-col gap-2 overflow-y-auto bg-[--zello-emerald]/[0.04] px-[18px] pb-5 pt-[18px]"
        >
          {scenario.bubbles.map((b, i) => (
            <ChatBubble key={`${scenario.id}-${i}`} bubble={b} delay={i * 60} />
          ))}
        </div>
      </div>

      {/* navegação por dots */}
      <div className="mt-[18px] flex items-center justify-center gap-3">
        <button
          type="button"
          onClick={() =>
            setIndex((index - 1 + SCENARIOS.length) % SCENARIOS.length)
          }
          aria-label="Conversa anterior"
          className="inline-flex p-1.5 text-muted-foreground transition-colors hover:text-foreground"
        >
          <ArrowLeft className="h-[18px] w-[18px]" />
        </button>
        {SCENARIOS.map((sc, i) => (
          <button
            key={sc.id}
            type="button"
            onClick={() => setIndex(i)}
            aria-label={`Conversa ${i + 1}`}
            className="p-1.5"
          >
            <span
              className={`block h-2 rounded-full transition-all duration-300 ${
                i === index
                  ? "w-7 bg-[--zello-emerald]"
                  : "w-2 bg-[--zello-emerald]/25"
              }`}
            />
          </button>
        ))}
        <button
          type="button"
          onClick={() => setIndex((index + 1) % SCENARIOS.length)}
          aria-label="Próxima conversa"
          className="inline-flex p-1.5 text-muted-foreground transition-colors hover:text-foreground"
        >
          <ArrowRight className="h-[18px] w-[18px]" />
        </button>
      </div>

      {/* legenda de público */}
      <div className="mt-1.5 text-center text-[13px] text-muted-foreground">
        <span className="font-display font-medium italic text-[--zello-emerald-deep]">
          {scenario.audience}
        </span>
        {" · "}
        {scenario.caption}
      </div>
    </div>
  );
}

function ChatBubble({ bubble, delay }: { bubble: Bubble; delay: number }) {
  if (bubble.who === "system") {
    return (
      <div className="flex justify-center py-1">
        <span className="inline-flex items-center gap-1.5 rounded-full border border-border bg-[--zello-cream] px-3 py-1.5 text-[11px] text-muted-foreground">
          <ShieldCheck className="h-3 w-3 text-[--zello-emerald]" />
          {bubble.text}
        </span>
      </div>
    );
  }

  const isMe = bubble.who === "me";
  return (
    <div className={`flex ${isMe ? "justify-end" : "justify-start"}`}>
      <div
        className={`animate-bubble max-w-[82%] rounded-2xl px-3 py-2.5 text-[13.5px] leading-relaxed shadow-sm ${
          isMe
            ? "rounded-tr-md bg-[--zello-emerald] text-[--zello-cream]"
            : "rounded-tl-md bg-card text-card-foreground"
        }`}
        style={{ "--bubble-delay": `${delay}ms` } as React.CSSProperties}
      >
        {bubble.html ? (
          <span dangerouslySetInnerHTML={{ __html: bubble.html }} />
        ) : (
          <span>{bubble.text}</span>
        )}
        {bubble.time && (
          <span className="mt-[3px] flex items-center justify-end gap-1 text-[10px] opacity-65">
            {bubble.time}
            {isMe && <CheckCheck className="h-[11px] w-[11px]" />}
          </span>
        )}
      </div>
    </div>
  );
}
