import Link from "next/link";

import { Wordmark } from "@/components/brand/Logo";

export function SiteFooter() {
  const year = new Date().getFullYear();
  return (
    <footer className="border-t border-border/70 bg-secondary/40">
      <div className="container flex flex-col gap-8 py-12 md:flex-row md:items-start md:justify-between">
        <div className="max-w-xs space-y-3">
          <Wordmark />
          <p className="text-sm leading-relaxed text-muted-foreground">
            Um assistente atento — pra você, pra quem você ama, todo dia.
          </p>
        </div>
        <nav className="flex flex-col gap-2 text-sm">
          <span className="font-display text-base font-medium text-foreground">
            Confiança
          </span>
          <Link
            href="/privacidade"
            className="text-muted-foreground transition-colors hover:text-primary"
          >
            Privacidade
          </Link>
          <Link
            href="/termos"
            className="text-muted-foreground transition-colors hover:text-primary"
          >
            Termos de uso
          </Link>
        </nav>
      </div>
      <div className="border-t border-border/60">
        <div className="container py-5 text-xs text-muted-foreground">
          &copy; {year} Zello &middot; Itacitrus. Feito com cuidado, privacidade
          em primeiro lugar.
        </div>
      </div>
    </footer>
  );
}
