import Link from "next/link";

import { Wordmark } from "@/components/brand/Logo";

export function AuthShell({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="relative flex min-h-screen flex-col overflow-hidden">
      {/* forma organica sutil de fundo */}
      <div aria-hidden className="pointer-events-none absolute inset-0 -z-10">
        <div className="absolute -left-32 -top-24 h-[26rem] w-[26rem] rounded-full bg-[--zello-emerald]/12 blur-3xl" />
        <div className="absolute -right-24 bottom-0 h-80 w-80 rounded-full bg-[--zello-amber]/20 blur-3xl" />
        <div className="absolute inset-0 bg-noise opacity-40 mix-blend-multiply" />
      </div>

      <header className="border-b border-border/70 bg-[--zello-cream]/70 backdrop-blur-md">
        <div className="container flex h-16 items-center">
          <Link
            href="/"
            className="rounded-md ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
          >
            <Wordmark />
          </Link>
        </div>
      </header>

      <main className="flex flex-1 items-center justify-center p-4 py-12">
        <div className="w-full max-w-md">
          <div className="mb-6 text-center">
            <h1 className="text-3xl font-semibold tracking-tight">{title}</h1>
            {subtitle ? (
              <p className="mt-2 text-balance text-muted-foreground">
                {subtitle}
              </p>
            ) : null}
          </div>
          <div className="rounded-[1.5rem] border border-border bg-card p-6 shadow-warm sm:p-8">
            {children}
          </div>
        </div>
      </main>
    </div>
  );
}
