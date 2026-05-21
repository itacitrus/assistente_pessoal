"use client";

import * as React from "react";
import { useRouter } from "next/navigation";

/**
 * Quando a síntese ainda está sendo preparada (geração assíncrona no backend),
 * recarrega os dados do servidor uma vez após um curto intervalo, até um teto
 * de tentativas. Assim a página "se completa" sozinha sem o usuário precisar
 * dar F5, e sem ficar em loop infinito se a geração falhar.
 */
export function SynthesisAutoRefresh({
  pending,
  intervalMs = 6000,
  maxAttempts = 3,
}: {
  pending: boolean;
  intervalMs?: number;
  maxAttempts?: number;
}) {
  const router = useRouter();
  const attempts = React.useRef(0);

  React.useEffect(() => {
    if (!pending) return;
    if (attempts.current >= maxAttempts) return;
    const t = setTimeout(() => {
      attempts.current += 1;
      router.refresh();
    }, intervalMs);
    return () => clearTimeout(t);
  }, [pending, intervalMs, maxAttempts, router]);

  return null;
}
