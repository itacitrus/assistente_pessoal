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
          <Button asChild variant="ghost">
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
