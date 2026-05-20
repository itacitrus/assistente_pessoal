import Link from "next/link";

import { Button } from "@/components/ui/button";

export default function LandingPage() {
  return (
    <div className="flex min-h-screen flex-col">
      <header className="border-b">
        <div className="container flex h-16 items-center justify-between">
          <span className="text-lg font-semibold">Assistente</span>
          <nav className="flex items-center gap-2">
            <Button asChild variant="ghost">
              <Link href="/login">Entrar</Link>
            </Button>
            <Button asChild>
              <Link href="/signup">Criar conta</Link>
            </Button>
          </nav>
        </div>
      </header>

      <main className="flex-1">
        <section className="container py-16 md:py-24">
          <div className="mx-auto max-w-2xl text-center">
            <h1 className="text-balance text-4xl font-semibold tracking-tight md:text-5xl">
              Sua agenda em boas maos.
            </h1>
            <p className="mt-6 text-balance text-lg text-muted-foreground">
              O assistente cuida do calendario da sua familia pelo WhatsApp,
              sem app pra instalar. Conversa em portugues normal, lembra dos
              compromissos, acompanha medicacoes e te avisa quando algo precisa
              de atencao.
            </p>
            <div className="mt-8 flex flex-col items-center justify-center gap-3 sm:flex-row">
              <Button asChild size="lg">
                <Link href="/signup">Criar conta</Link>
              </Button>
              <Button asChild size="lg" variant="outline">
                <Link href="/login">Ja tenho conta</Link>
              </Button>
            </div>
          </div>
        </section>

        <section className="border-t bg-muted/30">
          <div className="container py-16">
            <h2 className="text-center text-2xl font-semibold tracking-tight">
              Como funciona
            </h2>
            <ol className="mx-auto mt-8 grid max-w-3xl gap-6 md:grid-cols-3">
              <Step
                n={1}
                title="Voce cria uma conta"
                body="Cadastra seu numero de WhatsApp e diz se vai usar pra cuidar da sua propria agenda ou de alguem."
              />
              <Step
                n={2}
                title="O assistente entra em contato"
                body="Voce recebe uma mensagem do bot no WhatsApp. A partir dai, basta conversar."
              />
              <Step
                n={3}
                title="Pronto"
                body="Fala com ele em portugues normal: 'marca consulta sexta as 14h', 'lembra de tomar o remedio', 'como esta o vovo?'."
              />
            </ol>
          </div>
        </section>

        <section className="container py-16">
          <div className="mx-auto max-w-2xl text-center">
            <h2 className="text-2xl font-semibold tracking-tight">
              Pensado pra cuidar de quem voce ama
            </h2>
            <p className="mt-4 text-muted-foreground">
              Pra responsaveis: o assistente acompanha as conversas com seu
              ente querido, identifica padroes (humor, energia, sociabilidade,
              autocuidado) e gera uma sintese profissional. Sem violar a
              privacidade — voce ve sinais agregados, nao mensagens literais.
            </p>
          </div>
        </section>
      </main>

      <footer className="border-t py-8 text-center text-sm text-muted-foreground">
        <div className="container">
          Feito com cuidado. Privacidade em primeiro lugar.
        </div>
      </footer>
    </div>
  );
}

function Step({ n, title, body }: { n: number; title: string; body: string }) {
  return (
    <li className="flex flex-col items-start gap-2 rounded-lg border bg-background p-6">
      <span className="flex h-8 w-8 items-center justify-center rounded-full bg-primary text-sm font-semibold text-primary-foreground">
        {n}
      </span>
      <h3 className="text-lg font-medium">{title}</h3>
      <p className="text-sm text-muted-foreground">{body}</p>
    </li>
  );
}
