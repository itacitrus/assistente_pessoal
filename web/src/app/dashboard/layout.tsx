import Link from "next/link";
import { redirect } from "next/navigation";

import { ApiError } from "@/lib/api";
import { getMe } from "@/lib/api/auth";
import { logoutAction } from "@/app/dashboard/actions";
import { getSessionCookieHeader } from "@/lib/server-cookie";
import { Wordmark } from "@/components/brand/Logo";
import { ImpersonationBanner } from "@/components/admin/ImpersonationBanner";
import type { User } from "@/types/api";

export default async function DashboardLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  const cookieHeader = getSessionCookieHeader();
  if (!cookieHeader) {
    redirect("/login");
  }

  let me: User;
  try {
    me = await getMe(cookieHeader);
  } catch (err) {
    if (err instanceof ApiError && err.isUnauthorized) {
      redirect("/login");
    }
    throw err;
  }

  return (
    <div className="flex min-h-screen flex-col">
      <header className="sticky top-0 z-40 border-b border-border/70 bg-[--zello-cream]/80 backdrop-blur-md">
        <div className="container flex h-16 items-center justify-between">
          <Link
            href="/dashboard"
            className="rounded-md ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
          >
            <Wordmark />
          </Link>
          <nav className="flex items-center gap-4 text-sm">
            <Link
              href="/dashboard"
              className="text-muted-foreground hover:text-foreground"
            >
              Início
            </Link>
            <Link
              href="/dashboard/preferences"
              className="text-muted-foreground hover:text-foreground"
            >
              Preferências
            </Link>
            {me.is_admin ? (
              <Link
                href="/dashboard/admin"
                className="font-medium text-[--zello-emerald] hover:text-[--zello-emerald-deep]"
              >
                Admin
              </Link>
            ) : null}
            <span className="hidden text-muted-foreground sm:inline">
              {me.name}
            </span>
            <form action={logoutAction}>
              <button
                type="submit"
                className="text-sm font-medium text-foreground underline-offset-4 hover:underline"
              >
                Sair
              </button>
            </form>
          </nav>
        </div>
      </header>
      {me.viewing_as ? <ImpersonationBanner name={me.viewing_as.name} /> : null}
      <main className="container flex-1 py-8">{children}</main>
    </div>
  );
}
