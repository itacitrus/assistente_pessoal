import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import type { NivelPreocupacao, SynthesisSummary } from "@/types/api";

export interface SynthesisCardProps {
  synthesis: SynthesisSummary;
  /** false quando a síntese ainda não foi gerada (idoso novo). A geração roda
   * em background; a página se atualiza sozinha em instantes. */
  available?: boolean;
}

export function SynthesisCard({
  synthesis,
  available = true,
}: SynthesisCardProps) {
  // Síntese ainda não gerada (persistida ausente). A geração acontece fora do
  // request (assíncrona) — mostramos um estado "preparando" em vez de travar a
  // página. Um auto-refresh leve no page recarrega quando ficar pronta.
  if (!available) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Síntese recente</CardTitle>
          <CardDescription>
            Estamos preparando a síntese — ela aparece sozinha em instantes.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }

  // Backend sempre retorna sintese — quando nao ha dados suficientes,
  // tendencia="indeterminado" e nivel_preocupacao="indeterminado".
  const isIndeterminate =
    synthesis.tendencia === "indeterminado" && !synthesis.resumo;

  if (isIndeterminate) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Síntese recente</CardTitle>
          <CardDescription>
            Ainda não há conversas suficientes para gerar uma síntese.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Síntese recente</CardTitle>
        <CardDescription>{nivelLabel(synthesis.nivel_preocupacao)}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {synthesis.resumo && (
          <p className="text-base leading-relaxed">{synthesis.resumo}</p>
        )}

        {synthesis.comparacao && (
          <p className="text-sm text-muted-foreground">{synthesis.comparacao}</p>
        )}

        {synthesis.ponto_de_atencao && (
          <section>
            <h3 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">
              Ponto de atenção
            </h3>
            <p className="mt-2 text-base">{synthesis.ponto_de_atencao}</p>
          </section>
        )}

        {synthesis.recomendacoes_carinhosas &&
          synthesis.recomendacoes_carinhosas.length > 0 && (
            <section>
              <h3 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">
                Recomendações
              </h3>
              <ul className="mt-2 list-disc space-y-1 pl-5 text-base">
                {synthesis.recomendacoes_carinhosas.map((r, i) => (
                  <li key={i}>{r}</li>
                ))}
              </ul>
            </section>
          )}
      </CardContent>
    </Card>
  );
}

function nivelLabel(nivel: NivelPreocupacao): string {
  switch (nivel) {
    case "tranquilo":
      return "Situação tranquila.";
    case "atencao":
      return "Vale ficar atento.";
    case "atencao_alta":
      return "Atenção redobrada.";
    case "indeterminado":
      return "Sem dados suficientes para classificar.";
  }
}
