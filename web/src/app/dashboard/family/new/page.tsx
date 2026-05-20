import { DependentForm } from "@/components/forms/DependentForm";

export const metadata = {
  title: "Adicionar pessoa — Assistente",
};

export default function NewDependentPage() {
  return (
    <div className="mx-auto max-w-md space-y-6">
      <header>
        <h1 className="text-3xl font-semibold tracking-tight">
          Adicionar pessoa que voce cuida
        </h1>
        <p className="mt-2 text-sm text-muted-foreground">
          O assistente vai conversar com essa pessoa pelo WhatsApp e voce
          recebe sinteses periodicas.
        </p>
      </header>
      <DependentForm />
    </div>
  );
}
