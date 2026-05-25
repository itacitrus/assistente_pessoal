"use client";

import * as React from "react";
import {
  Line,
  LineChart,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import type { SnapshotPoint } from "@/types/api";

export interface PsychTimelineProps {
  snapshots: SnapshotPoint[];
}

interface ChartConfig {
  key: keyof Pick<
    SnapshotPoint,
    "humor" | "energia" | "sociabilidade" | "autocuidado"
  >;
  label: string;
  color: string;
}

const CHARTS: ChartConfig[] = [
  { key: "humor", label: "Humor", color: "#16a34a" },
  { key: "energia", label: "Energia", color: "#2563eb" },
  { key: "sociabilidade", label: "Sociabilidade", color: "#9333ea" },
  { key: "autocuidado", label: "Autocuidado", color: "#ea580c" },
];

interface ChartPoint {
  date: string;
  dateLabel: string;
  value: number | null;
  confidence: number | null;
}

// formatDayMonth converte "YYYY-MM-DD" em "dd/MM" tratando a string como data
// de calendario (sem fuso). Evita `new Date(iso)`, que interpreta a data como
// UTC e, ao formatar em BRT (UTC-3), recua um dia — e, pior, em entrada vazia
// ou invalida produzia o epoch ("31/12"). Retorna "" se a string nao casar.
function formatDayMonth(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(iso ?? "");
  if (!m) return "";
  return `${m[3]}/${m[2]}`;
}

export function PsychTimeline({ snapshots }: PsychTimelineProps) {
  if (snapshots.length === 0) {
    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-base">Linha do tempo</CardTitle>
          <CardDescription>
            Ainda não há dados de evolução. Aguarde algumas conversas para
            ver a linha do tempo.
          </CardDescription>
        </CardHeader>
      </Card>
    );
  }

  return (
    <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
      {CHARTS.map((cfg) => (
        <SingleChart key={cfg.key} cfg={cfg} snapshots={snapshots} />
      ))}
    </div>
  );
}

function SingleChart({
  cfg,
  snapshots,
}: {
  cfg: ChartConfig;
  snapshots: SnapshotPoint[];
}) {
  const data: ChartPoint[] = (snapshots ?? []).map((s) => ({
    date: s.date,
    dateLabel: formatDayMonth(s.date),
    value: s[cfg.key],
    confidence: s.confidence,
  }));

  const hasAny = data.some((p) => p.value !== null);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{cfg.label}</CardTitle>
        <CardDescription>Escala 1 a 5. Linha tracejada = 3.</CardDescription>
      </CardHeader>
      <CardContent>
        {!hasAny ? (
          <p className="py-12 text-center text-sm text-muted-foreground">
            Sem dados nesta janela.
          </p>
        ) : (
          <div className="h-56 w-full">
            <ResponsiveContainer width="100%" height="100%">
              <LineChart
                data={data}
                margin={{ top: 8, right: 12, bottom: 0, left: -16 }}
              >
                <XAxis
                  dataKey="dateLabel"
                  fontSize={12}
                  tick={{ fill: "hsl(var(--muted-foreground))" }}
                />
                <YAxis
                  domain={[1, 5]}
                  ticks={[1, 2, 3, 4, 5]}
                  fontSize={12}
                  tick={{ fill: "hsl(var(--muted-foreground))" }}
                />
                <ReferenceLine
                  y={3}
                  stroke="hsl(var(--muted-foreground))"
                  strokeDasharray="3 3"
                />
                <Tooltip
                  content={(props: unknown) => {
                    const p = props as {
                      active?: boolean;
                      payload?: Array<{ payload: ChartPoint }>;
                    };
                    return (
                      <CustomTooltip
                        active={p.active}
                        payload={p.payload}
                        label={cfg.label}
                      />
                    );
                  }}
                />
                <Line
                  type="monotone"
                  dataKey="value"
                  stroke={cfg.color}
                  strokeWidth={2}
                  connectNulls
                  dot={(dotProps: unknown) => (
                    <ConfidenceDot
                      {...(dotProps as ConfidenceDotProps)}
                      color={cfg.color}
                    />
                  )}
                  activeDot={{ r: 5 }}
                />
              </LineChart>
            </ResponsiveContainer>
          </div>
        )}
      </CardContent>
    </Card>
  );
}

interface ConfidenceDotProps {
  cx?: number;
  cy?: number;
  payload?: ChartPoint;
  index?: number;
  color: string;
}

function ConfidenceDot({ cx, cy, payload, color, index }: ConfidenceDotProps) {
  if (cx === undefined || cy === undefined || !payload) return <g />;
  if (payload.value === null) return <g />;
  const conf = payload.confidence ?? 3;
  const opacity = conf < 3 ? 0.4 : 1;
  return (
    <circle
      key={index}
      cx={cx}
      cy={cy}
      r={3.5}
      fill={color}
      opacity={opacity}
    />
  );
}

function CustomTooltip({
  active,
  payload,
  label,
}: {
  active?: boolean;
  payload?: Array<{ payload: ChartPoint }>;
  label: string;
}) {
  if (!active || !payload || payload.length === 0) return null;
  const point = payload[0].payload;
  if (point.value === null) return null;

  const confLabel =
    point.confidence === null
      ? "sem confiança registrada"
      : confidenceLabel(point.confidence);

  return (
    <div className="rounded-md border bg-popover p-2 text-sm shadow">
      <p className="font-medium">{label}</p>
      <p className="text-muted-foreground">{point.dateLabel}</p>
      <p>
        Nota: <span className="font-semibold">{point.value}</span>
      </p>
      <p className="text-xs text-muted-foreground">{confLabel}</p>
    </div>
  );
}

function confidenceLabel(c: number): string {
  if (c <= 1) return "confiança muito baixa";
  if (c <= 2) return "confiança baixa";
  if (c <= 3) return "confiança média";
  if (c <= 4) return "confiança alta";
  return "confiança muito alta";
}
