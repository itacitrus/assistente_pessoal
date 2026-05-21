import type { Metadata } from "next";
import { Fraunces, Hanken_Grotesk } from "next/font/google";

import "./globals.css";

const hanken = Hanken_Grotesk({
  subsets: ["latin"],
  variable: "--font-sans",
  display: "swap",
});

const fraunces = Fraunces({
  subsets: ["latin"],
  variable: "--font-display",
  display: "swap",
  axes: ["opsz", "SOFT"],
});

export const metadata: Metadata = {
  title: "Zello — cuidar de quem você ama, sem complicação",
  description:
    "Zello e o assistente no WhatsApp que cuida da agenda, lembra dos remedios, faz companhia ao idoso e mantem a familia tranquila.",
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="pt-BR">
      <body
        className={`${hanken.variable} ${fraunces.variable} font-sans antialiased`}
      >
        {children}
      </body>
    </html>
  );
}
