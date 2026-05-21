import type { ReactNode } from "react";

import { SiteHeader } from "@/components/site/SiteHeader";
import { SiteFooter } from "@/components/site/SiteFooter";

export function LegalDoc({
  title,
  updated,
  children,
}: {
  title: string;
  updated: string;
  children: ReactNode;
}) {
  return (
    <div className="flex min-h-screen flex-col">
      <SiteHeader />

      <main className="flex-1">
        <article
          className={[
            "container max-w-3xl py-14 md:py-20",
            "[&_h2]:mt-12 [&_h2]:scroll-mt-24 [&_h2]:font-display [&_h2]:text-[22px] [&_h2]:font-semibold [&_h2]:tracking-[-0.01em] [&_h2]:text-foreground md:[&_h2]:text-2xl",
            "[&_h3]:mt-7 [&_h3]:font-display [&_h3]:text-[17px] [&_h3]:font-semibold [&_h3]:text-foreground",
            "[&_p]:mt-4 [&_p]:leading-[1.7] [&_p]:text-muted-foreground",
            "[&_ul]:mt-4 [&_ul]:list-disc [&_ul]:space-y-2 [&_ul]:pl-5 [&_ul]:marker:text-[--zello-emerald]",
            "[&_li]:leading-[1.65] [&_li]:text-muted-foreground",
            "[&_a]:font-medium [&_a]:text-[--zello-emerald] [&_a]:underline [&_a]:underline-offset-2 hover:[&_a]:text-[--zello-emerald-deep]",
            "[&_strong]:font-semibold [&_strong]:text-foreground",
            "[&_em]:italic",
            "[&_code]:rounded [&_code]:bg-secondary [&_code]:px-1.5 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[0.85em] [&_code]:text-foreground",
            "[&_blockquote]:mt-6 [&_blockquote]:rounded-r-xl [&_blockquote]:border-l-2 [&_blockquote]:border-[--zello-emerald]/50 [&_blockquote]:bg-secondary/50 [&_blockquote]:py-3 [&_blockquote]:pl-5 [&_blockquote]:pr-4 [&_blockquote_p]:mt-0 [&_blockquote_p]:text-foreground/80",
          ].join(" ")}
        >
          <h1 className="font-display text-3xl font-semibold tracking-[-0.02em] text-foreground md:text-[40px]">
            {title}
          </h1>
          <p className="mt-3 text-sm text-muted-foreground/80">{updated}</p>

          {children}
        </article>
      </main>

      <SiteFooter />
    </div>
  );
}
