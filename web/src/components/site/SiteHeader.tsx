import Link from "next/link";

import { Button } from "@/components/ui/button";
import { Wordmark } from "@/components/brand/Logo";

export function SiteHeader() {
  return (
    <header className="sticky top-0 z-40 border-b border-border/70 bg-[--zello-cream]/80 backdrop-blur-md supports-[backdrop-filter]:bg-[--zello-cream]/70">
      <div className="container flex h-16 items-center justify-between">
        <Link href="/" className="rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 ring-offset-background">
          <Wordmark />
        </Link>
        <nav className="flex items-center gap-1.5 sm:gap-2">
          <a
            href="/#como-funciona"
            className="hidden rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:text-foreground md:inline-flex"
          >
            Como funciona
          </a>
          <a
            href="/#privacidade"
            className="hidden rounded-md px-3 py-2 text-sm text-muted-foreground transition-colors hover:text-foreground md:inline-flex"
          >
            Privacidade
          </a>
          <Button asChild variant="ghost" className="hidden sm:inline-flex">
            <Link href="/login">Entrar</Link>
          </Button>
          <Button asChild>
            <Link href="/signup">Criar conta</Link>
          </Button>
        </nav>
      </div>
    </header>
  );
}
