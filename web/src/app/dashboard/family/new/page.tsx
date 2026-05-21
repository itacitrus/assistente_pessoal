import { DependentForm } from "@/components/forms/DependentForm";

export const metadata = {
  title: "Adicionar pessoa — Zello",
};

export default function NewDependentPage() {
  return (
    <div className="mx-auto max-w-md space-y-6">
      <header>
        <h1 className="text-3xl font-semibold tracking-tight">
          Adicionar pessoa que você cuida
        </h1>
        <p className="mt-2 text-sm text-muted-foreground">
          O Zello vai conversar com essa pessoa pelo WhatsApp e você recebe
          sínteses periódicas.
        </p>
      </header>
      <DependentForm />
    </div>
  );
}
