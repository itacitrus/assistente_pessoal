"use client";

import * as React from "react";
import { useRouter } from "next/navigation";

/**
 * Quando algum dado da página ainda está sendo gerado em background (síntese,
 * insights), recarrega os dados do servidor após um curto intervalo, até um
 * teto de tentativas. A página "se completa" sozinha sem F5, e não entra em
 * loop infinito se a geração falhar.
 */
export function PendingAutoRefresh({
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
