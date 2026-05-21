import { SignupForm } from "@/components/forms/SignupForm";
import { AuthShell } from "@/components/site/AuthShell";

export const metadata = {
  title: "Criar conta — Zello",
};

export default function SignupPage() {
  return (
    <AuthShell
      title="Crie sua conta"
      subtitle="Em poucos minutos o Zello comeca a cuidar de quem voce ama."
    >
      <SignupForm />
    </AuthShell>
  );
}
