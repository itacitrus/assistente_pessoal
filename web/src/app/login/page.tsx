import Link from "next/link";

import { LoginForm } from "@/components/forms/LoginForm";

export const metadata = {
  title: "Entrar — Assistente",
};

export default function LoginPage() {
  return (
    <div className="flex min-h-screen flex-col">
      <header className="border-b">
        <div className="container flex h-16 items-center justify-between">
          <Link href="/" className="text-lg font-semibold">
            Assistente
          </Link>
        </div>
      </header>
      <main className="flex flex-1 items-center justify-center p-4">
        <div className="w-full max-w-md">
          <h1 className="mb-6 text-3xl font-semibold tracking-tight">
            Entrar
          </h1>
          <LoginForm />
        </div>
      </main>
    </div>
  );
}
