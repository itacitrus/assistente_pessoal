import { LoginForm } from "@/components/forms/LoginForm";
import { AuthShell } from "@/components/site/AuthShell";

export const metadata = {
  title: "Entrar — Zello",
};

export default function LoginPage() {
  return (
    <AuthShell
      title="Bem-vindo de volta"
      subtitle="Entre para acompanhar o cuidado de quem você ama."
    >
      <LoginForm />
    </AuthShell>
  );
}
