import { useQuery } from "@tanstack/react-query";
import { ExternalLink, Maximize2, Minimize2 } from "lucide-react";
import { useTheme } from "next-themes";
import { useEffect, useMemo, useRef, useState } from "react";
import { useParams } from "react-router";

import { IdentityTabs } from "~/components/identity-tabs";
import { LoadingDots } from "~/components/ui/loading-dots";
import { fetchActivity, fetchIdentityStatus } from "~/lib/api";
import { toCard, type CardGroup, type Ml2Card } from "~/lib/mindlog2-model";
import { useMindlog } from "~/lib/use-mindlog";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "mindloop · 思维日志 v2" }];
}

/** Presentation stream: newest first, so a calm page with no scroll chasing. */
const MAX_CARDS = 80;
/** Clamp long bodies; the video's one-liners are not how Audel writes. */
const CLAMP_CHARS = 420;

const LEGEND: { group: CardGroup; label: string; dot: string }[] = [
  { group: "thought", label: "thought", dot: "bg-violet-400" },
  { group: "observation", label: "observation", dot: "bg-pink-500" },
  { group: "message", label: "message", dot: "bg-teal-300" },
];

const CARD_STYLE: Record<Ml2Card["kind"], { card: string; label: string }> = {
  thought: {
    card: "border-l-4 border-violet-500 bg-white/[0.04]",
    label: "text-violet-300",
  },
  observation: {
    card: "border-l-4 border-pink-500 bg-white/[0.04]",
    label: "text-pink-400",
  },
  outbound: {
    card: "ml-auto max-w-2xl border border-teal-400/40 bg-teal-400/15",
    label: "text-teal-300",
  },
  inbound: {
    card: "max-w-2xl border border-indigo-400/40 bg-indigo-400/20",
    label: "text-indigo-300",
  },
};

const CARD_STYLE_LIGHT: Record<Ml2Card["kind"], { card: string; label: string }> = {
  thought: {
    card: "border-l-4 border-violet-400 bg-violet-50",
    label: "text-violet-700",
  },
  observation: {
    card: "border-l-4 border-pink-400 bg-pink-50",
    label: "text-pink-700",
  },
  outbound: {
    card: "ml-auto max-w-2xl border border-teal-300 bg-teal-50",
    label: "text-teal-700",
  },
  inbound: {
    card: "max-w-2xl border border-indigo-300 bg-indigo-50",
    label: "text-indigo-700",
  },
};

function fmtTime(ts: string): string {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString([], {
    hour12: false,
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

const ACTIVITY_LABELS: Record<string, string> = {
  working: "工作中",
  stalled: "停滞",
  idle: "空闲",
  asleep: "休眠",
};

function StatusPill({ identityId, light }: { identityId: string; light: boolean }) {
  const { data: activity } = useQuery({
    queryKey: ["activity", identityId],
    queryFn: () => fetchActivity(identityId),
    refetchInterval: 2000,
  });
  const state = activity?.state ?? "asleep";
  const on = state === "working";
  const stalled = state === "stalled";
  return (
    <span
      className={cn(
        "flex items-center gap-2 rounded-full border px-3.5 py-1 font-mono text-sm",
        on && (light ? "border-green-500/60 text-green-700" : "border-green-400/50 text-green-300"),
        stalled &&
          (light ? "border-amber-500/60 text-amber-700" : "border-amber-400/50 text-amber-300"),
        !on && !stalled && (light ? "border-zinc-400 text-zinc-500" : "border-zinc-600 text-zinc-400")
      )}
    >
      <span className="relative flex h-2 w-2">
        {on && (
          <span
            className={cn(
              "absolute inline-flex h-full w-full animate-ping rounded-full opacity-75",
              light ? "bg-green-500" : "bg-green-400"
            )}
          />
        )}
        <span
          className={cn(
            "relative inline-flex h-2 w-2 rounded-full",
            on
              ? light
                ? "bg-green-500"
                : "bg-green-400"
              : stalled
                ? light
                  ? "bg-amber-500"
                  : "bg-amber-400"
                : light
                  ? "bg-zinc-400"
                  : "bg-zinc-500"
          )}
        />
      </span>
      {ACTIVITY_LABELS[state] ?? state}
    </span>
  );
}

function StreamCard({
  card,
  animate,
  light,
}: {
  card: Ml2Card;
  animate: boolean;
  light: boolean;
}) {
  const [expanded, setExpanded] = useState(false);
  const long = card.body.length > CLAMP_CHARS;
  const body = expanded || !long ? card.body : `${card.body.slice(0, CLAMP_CHARS)}…`;
  const style = (light ? CARD_STYLE_LIGHT : CARD_STYLE)[card.kind];
  return (
    <div
      className={cn(
        "w-full max-w-3xl rounded-2xl px-6 py-5 shadow-lg",
        light ? "shadow-black/[0.06]" : "shadow-black/20",
        style.card,
        animate && "ml2-enter",
        long && "cursor-pointer"
      )}
      onClick={long ? () => setExpanded((v) => !v) : undefined}
      title={long && !expanded ? "click to expand" : undefined}
    >
      <div className="mb-2 flex items-baseline gap-4">
        <span className={cn("font-mono text-sm tracking-wide", style.label)}>
          {card.label}
        </span>
        {card.source_url && (
          <a
            href={card.source_url}
            target="_blank"
            rel="noreferrer"
            className={cn(
              "inline-flex items-center gap-1 font-mono text-xs hover:underline",
              light ? "text-zinc-500 hover:text-zinc-900" : "text-zinc-400 hover:text-zinc-100"
            )}
            onClick={(event) => event.stopPropagation()}
          >
            Open in Slack <ExternalLink className="h-3 w-3" />
          </a>
        )}
        <span className="ml-auto font-mono text-sm tabular-nums text-zinc-500">
          {fmtTime(card.ts)}
        </span>
      </div>
      <div
        className={cn(
          "whitespace-pre-wrap text-lg leading-snug md:text-2xl",
          light ? "text-zinc-800" : "text-zinc-100"
        )}
      >
        {body}
      </div>
    </div>
  );
}

export default function Mindlog2Page() {
  const { identityId = "" } = useParams();
  const [hidden, setHidden] = useState<Set<CardGroup>>(new Set());
  const [fullscreen, setFullscreen] = useState(false);

  // SPA: resolvedTheme is undefined until next-themes hydrates; render dark
  // (the canvas's native look) until mounted, then follow the site theme.
  const { resolvedTheme } = useTheme();
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  const light = mounted && resolvedTheme === "light";

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: 2000,
  });
  const live = status?.live ?? false;

  const { data: mindlog, isLoading } = useMindlog(identityId, live);

  // Steps already held when the page first rendered don't animate; only
  // steps that arrive live afterwards slide in.
  const initialEndRef = useRef<number | null>(null);
  useEffect(() => {
    if (mindlog && initialEndRef.current === null) {
      initialEndRef.current = mindlog.start + mindlog.steps.length;
    }
  }, [mindlog]);

  const cards = useMemo(() => {
    if (!mindlog) return [];
    const name = mindlog.identity.name;
    const out: { card: Ml2Card; abs: number }[] = [];
    for (let i = mindlog.steps.length - 1; i >= 0 && out.length < MAX_CARDS; i--) {
      const card = toCard(mindlog.steps[i], name);
      if (card && !hidden.has(card.group)) {
        out.push({ card, abs: mindlog.start + i });
      }
    }
    return out;
  }, [mindlog, hidden]);

  const counts = useMemo(() => {
    const map = new Map<CardGroup, number>();
    if (!mindlog) return map;
    const name = mindlog.identity.name;
    for (const step of mindlog.steps) {
      const card = toCard(step, name);
      if (card) map.set(card.group, (map.get(card.group) ?? 0) + 1);
    }
    return map;
  }, [mindlog]);

  const toggle = (group: CardGroup) => {
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(group)) next.delete(group);
      else next.add(group);
      return next;
    });
  };

  useEffect(() => {
    if (!fullscreen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setFullscreen(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [fullscreen]);

  const displayName = mindlog?.identity.name ?? identityId.split("~").pop() ?? identityId;

  const canvas = (
    <div
      className={cn(
        "ml2-canvas relative",
        light ? "text-zinc-800" : "text-zinc-100",
        fullscreen
          ? "min-h-full px-6 py-6 md:px-12"
          : cn(
              "min-h-[80vh] rounded-2xl border px-5 py-5 md:px-10 md:py-8",
              light ? "border-zinc-200" : "border-zinc-800"
            )
      )}
    >
      <header className="flex items-center gap-4">
        <span
          className={cn(
            "text-2xl font-bold tracking-tight",
            light ? "text-zinc-900" : "text-white"
          )}
        >
          mindloop
        </span>
        <div className="ml-auto flex items-center gap-3">
          <span className={cn("font-mono text-lg", light ? "text-zinc-600" : "text-zinc-300")}>
            {displayName}
          </span>
          <StatusPill identityId={identityId} light={light} />
          <button
            type="button"
            onClick={() => setFullscreen((v) => !v)}
            className={cn(
              "rounded-md border p-1.5",
              light
                ? "border-zinc-300 text-zinc-500 hover:text-zinc-900"
                : "border-zinc-700 text-zinc-400 hover:text-zinc-100"
            )}
            title={fullscreen ? "退出全屏（Esc）" : "全屏"}
          >
            {fullscreen ? <Minimize2 className="h-4 w-4" /> : <Maximize2 className="h-4 w-4" />}
          </button>
        </div>
      </header>
      <div className={cn("mt-4 border-t", light ? "border-zinc-200" : "border-zinc-800")} />

      <div className="mt-6 flex gap-8">
        <aside className="hidden w-44 shrink-0 pt-2 sm:block">
          <h3 className="mb-3 font-mono text-xs uppercase tracking-[0.2em] text-zinc-500">
            步骤类型
          </h3>
          <div className="space-y-2.5">
            {LEGEND.map(({ group, label, dot }) => (
              <button
                key={group}
                type="button"
                onClick={() => toggle(group)}
                className={cn(
                  "flex w-full items-center gap-2.5 font-mono text-sm",
                  hidden.has(group)
                    ? "text-zinc-600 line-through"
                    : light
                      ? "text-zinc-700"
                      : "text-zinc-200"
                )}
              >
                <span className={cn("h-2 w-2 rounded-sm", dot)} />
                <span className="flex-1 text-left">{label}</span>
                <span className="tabular-nums text-zinc-500">
                  {counts.get(group) ?? 0}
                </span>
              </button>
            ))}
          </div>
        </aside>

        <main
          className={cn(
            "min-w-0 flex-1 border-l pl-6 md:pl-10",
            light ? "border-zinc-200" : "border-zinc-800"
          )}
        >
          <div className="flex items-baseline">
            <h2
              className={cn(
                "text-4xl font-extrabold tracking-tight md:text-5xl",
                light ? "text-zinc-900" : "text-white"
              )}
            >
              思维日志
            </h2>
            <span className="ml-auto font-mono text-sm text-zinc-500">
              {live ? "实时流" : "未运行"}
            </span>
          </div>

          <div className="mt-6 flex flex-col gap-5 pb-10">
            {cards.length === 0 && (
              <div className="py-16 text-center font-mono text-sm text-zinc-500">
                waiting for thoughts…
              </div>
            )}
            {cards.map(({ card, abs }) => (
              <StreamCard
                key={card.step_id || abs}
                card={card}
                light={light}
                animate={initialEndRef.current !== null && abs >= initialEndRef.current}
              />
            ))}
          </div>
        </main>
      </div>
    </div>
  );

  if (isLoading) {
    return (
      <div className="flex justify-center py-20">
        <LoadingDots />
      </div>
    );
  }

  if (fullscreen) {
    return (
      <div
        className={cn(
          "fixed inset-0 z-50 overflow-y-auto",
          light ? "bg-[#f7f7fb]" : "bg-[#0b0a10]"
        )}
      >
        {canvas}
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl">
      <IdentityTabs
        identityId={identityId}
        live={live}
        active="mindlog2"
        name={mindlog?.identity.name}
      />
      {canvas}
    </div>
  );
}
