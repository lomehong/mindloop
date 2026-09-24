import { useQuery } from "@tanstack/react-query";
import { Info, Search, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { StepCard } from "~/components/step-card";
import { Input } from "~/components/ui/input";
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "~/components/ui/tooltip";
import { fetchStep, searchMindlog } from "~/lib/api";
import { stepColor } from "~/lib/step-colors";
import type { SearchHit } from "~/lib/types";
import { cn } from "~/lib/utils";

const DEBOUNCE_MS = 300;

const SCOPE_HELP =
  '"thoughts" searches the mind-level steps: thoughts, messages, ' +
  'observations, actions. "everything" also searches run machinery: ' +
  "prompts, model reasoning, and shell output — noisier, but complete.";

function hitTime(ts: string | null): string {
  if (!ts) return "";
  const date = new Date(ts);
  if (Number.isNaN(date.getTime())) return "";
  return date.toLocaleString([], {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** Snippet with the first case-insensitive match emphasized. */
function Snippet({ text, q }: { text: string; q: string }) {
  const pos = text.toLowerCase().indexOf(q.toLowerCase());
  if (pos === -1) return <>{text}</>;
  return (
    <>
      {text.slice(0, pos)}
      <mark className="rounded-sm bg-amber-200 px-0.5 dark:bg-amber-900">
        {text.slice(pos, pos + q.length)}
      </mark>
      {text.slice(pos + q.length)}
    </>
  );
}

/** Modal for a step older than the loaded window (a search hit or a
 * deeplink): fetch and show the one step without dragging thousands of
 * intermediate steps into the page. */
export function StepModal({
  identityId,
  stepId,
  stepCount,
  onClose,
}: {
  identityId: string;
  stepId: string;
  stepCount?: number;
  onClose: () => void;
}) {
  const { data, isError } = useQuery({
    queryKey: ["step", identityId, stepId],
    queryFn: () => fetchStep(identityId, stepId),
    staleTime: Infinity,
  });
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4"
      onClick={onClose}
    >
      <div
        className="relative max-h-[80vh] w-full max-w-2xl overflow-y-auto rounded-lg border bg-background p-4 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <button
          type="button"
          aria-label="Close"
          className="absolute right-3 top-3 rounded p-1 hover:bg-accent"
          onClick={onClose}
        >
          <X className="h-4 w-4" />
        </button>
        {isError ? (
          <div className="py-6 text-sm text-muted-foreground">
            Step not found.
          </div>
        ) : !data ? (
          <div className="py-6 text-sm text-muted-foreground">loading…</div>
        ) : (
          <>
            <div className="mb-2 pr-8 font-mono text-[10px] text-muted-foreground">
              step {data.index + 1}
              {stepCount !== undefined && <> of {stepCount}</>} — older than
              the loaded window
            </div>
            <StepCard step={data.step} expandAll />
          </>
        )}
      </div>
    </div>
  );
}

export function MindlogSearch({
  identityId,
  windowStart,
  onJump,
}: {
  identityId: string;
  /** Absolute index of the first loaded step — hits at/after it are on
   * the page and scrollable; older hits open in a modal. */
  windowStart: number;
  onJump: (hit: SearchHit) => void;
}) {
  const [q, setQ] = useState("");
  const [scope, setScope] = useState<"thoughts" | "all">("thoughts");
  const [debouncedQ, setDebouncedQ] = useState("");
  const [open, setOpen] = useState(false);
  const [modalStep, setModalStep] = useState<string | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const timer = setTimeout(() => setDebouncedQ(q.trim()), DEBOUNCE_MS);
    return () => clearTimeout(timer);
  }, [q]);

  // Click-away closes the results panel.
  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (!boxRef.current?.contains(e.target as Node)) setOpen(false);
    };
    window.addEventListener("mousedown", onDown);
    return () => window.removeEventListener("mousedown", onDown);
  }, []);

  const enabled = debouncedQ.length >= 2;
  const { data, isFetching } = useQuery({
    queryKey: ["mindlog-search", identityId, debouncedQ, scope],
    queryFn: () => searchMindlog(identityId, debouncedQ, scope),
    enabled,
  });

  return (
    // ml-auto: when the sticky header wraps, keep the box (and its
    // right-anchored dropdown) against the right edge instead of letting
    // the dropdown clip off-screen left.
    <div ref={boxRef} className="relative ml-auto">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={q}
          onChange={(e) => {
            setQ(e.target.value);
            setOpen(true);
          }}
          onFocus={() => setOpen(true)}
          placeholder="Search…"
          className="h-8 w-44 pl-8 text-xs sm:w-64"
          autoComplete="off"
        />
      </div>

      {open && enabled && (
        <div className="absolute right-0 z-50 mt-1 w-[26rem] max-w-[90vw] rounded-lg border bg-background shadow-lg">
          <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs text-muted-foreground">
            <span>
              {data
                ? data.total > data.hits.length
                  ? `${data.hits.length} of ${data.total} matches`
                  : `${data.total} match${data.total === 1 ? "" : "es"}`
                : isFetching
                  ? "searching…"
                  : ""}
            </span>
            <div className="ml-auto flex items-center gap-1">
              {(["thoughts", "all"] as const).map((s) => (
                <button
                  key={s}
                  type="button"
                  onClick={() => setScope(s)}
                  className={cn(
                    "rounded px-1.5 py-0.5",
                    scope === s
                      ? "bg-accent font-medium text-foreground"
                      : "hover:text-foreground"
                  )}
                >
                  {s === "all" ? "everything" : "thoughts"}
                </button>
              ))}
              <TooltipProvider delayDuration={100}>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Info className="h-3.5 w-3.5 cursor-help" />
                  </TooltipTrigger>
                  <TooltipContent side="bottom" className="max-w-64 text-xs">
                    {SCOPE_HELP}
                  </TooltipContent>
                </Tooltip>
              </TooltipProvider>
            </div>
          </div>
          <div className="max-h-96 overflow-y-auto">
            {data && data.hits.length === 0 && !isFetching && (
              <div className="px-3 py-4 text-center text-xs text-muted-foreground">
                No matches.
              </div>
            )}
            {data?.hits.map((hit) => (
              <button
                key={hit.step_id}
                type="button"
                className="block w-full border-b px-3 py-2 text-left last:border-b-0 hover:bg-accent/50"
                onClick={() => {
                  if (hit.index >= windowStart) {
                    setOpen(false);
                    onJump(hit);
                  } else {
                    setModalStep(hit.step_id);
                  }
                }}
              >
                <div className="mb-0.5 flex items-center gap-2 font-mono text-[10px] text-muted-foreground">
                  <span
                    className={cn(
                      "rounded px-1 py-px",
                      stepColor(hit.type).chip
                    )}
                  >
                    {hit.type}
                  </span>
                  <span>{hitTime(hit.ts)}</span>
                  {hit.index < windowStart && <span>· not loaded</span>}
                </div>
                <div className="line-clamp-2 text-xs">
                  <Snippet text={hit.snippet} q={debouncedQ} />
                </div>
              </button>
            ))}
          </div>
        </div>
      )}

      {modalStep && data && (
        <StepModal
          identityId={identityId}
          stepId={modalStep}
          stepCount={data.step_count}
          onClose={() => setModalStep(null)}
        />
      )}
    </div>
  );
}
