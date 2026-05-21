import { Wordmark } from "@/components/brand/Logo";

export function SiteFooter() {
  const year = new Date().getFullYear();
  return (
    <footer className="border-t border-border/70 bg-secondary/40">
      <div className="container flex flex-col gap-8 py-12 md:flex-row md:items-start md:justify-between">
        <div className="max-w-xs space-y-3">
          <Wordmark />
          <p className="text-sm leading-relaxed text-muted-foreground">
            Cuidar de quem você ama, sem complicação.
          </p>
        </div>
        <nav className="flex flex-col gap-2 text-sm">
          <span className="font-display text-base font-medium text-foreground">
            Confianca
          </span>
          <a
            href="https://assistente.itacitrus.com.br/privacy"
            className="text-muted-foreground transition-colors hover:text-primary"
          >
            Privacidade
          </a>
          <a
            href="https://assistente.itacitrus.com.br/terms"
            className="text-muted-foreground transition-colors hover:text-primary"
          >
            Termos de uso
          </a>
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
