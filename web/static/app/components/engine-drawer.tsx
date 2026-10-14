// 引擎室抽屉（theater-stage.html drawer，舞台页专属）：「此刻状态」
// 只读读数——最近 5 条轨迹、在途任务、记忆计数。禁止内联操作（§4
// 职责表）；全屏审计走各引擎页。

import { useQuery } from "@tanstack/react-query";

import { fetchMemories, fetchTasks } from "~/lib/api";

type LogStep = { ts: string; type: string; source?: string; preview?: string };

const TYPE_COLOR: Record<string, string> = {
  message: "text-lake",
  shell: "text-moss",
  shell_output: "text-moss",
  reasoning: "text-plum",
  error: "text-destructive",
  scene: "text-resin",
  intent: "text-resin",
};

export function EngineDrawer({
  identityId,
  open,
  onClose,
}: {
  identityId: string;
  open: boolean;
  onClose: () => void;
}) {
  const { data: logs } = useQuery({
    queryKey: ["drawer-mindlog", identityId],
    queryFn: async (): Promise<LogStep[]> => {
      const res = await fetch(
        `/api/identities/${encodeURIComponent(identityId)}/mindlog?limit=5`
      );
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const j = (await res.json()) as { steps?: LogStep[] };
      return j.steps ?? [];
    },
    enabled: open,
    refetchInterval: 5000,
  });
  const { data: tasks } = useQuery({
    queryKey: ["drawer-tasks", identityId],
    queryFn: () => fetchTasks(identityId),
    enabled: open,
    refetchInterval: 5000,
  });
  const { data: memories } = useQuery({
    queryKey: ["drawer-memories", identityId],
    queryFn: () => fetchMemories(identityId),
    enabled: open,
    refetchInterval: 30_000,
  });

  const inFlight = (tasks ?? []).filter((t) =>
    ["queued", "running", "awaiting_approval"].includes(t.status)
  );

  return (
    <>
      {open && (
        <div
          className="absolute inset-0 z-[45] bg-black/35"
          onClick={onClose}
        />
      )}
      <div
        data-testid="engine-drawer"
        className={`absolute bottom-0 right-0 top-0 z-[46] flex w-[430px] max-w-[92vw] flex-col border-l border-line bg-[#0c120e] transition-transform duration-300 ${
          open ? "translate-x-0" : "translate-x-full"
        }`}
        style={{ transform: open ? undefined : "translateX(100%)" }}
      >
        <div className="flex items-center px-[18px] pb-2.5 pt-[14px]">
          <span className="flex-1 text-[13px] font-semibold">引擎室</span>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md border border-border px-2 py-0.5 text-xs text-muted-foreground hover:text-foreground"
          >
            收起
          </button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto px-[18px] pb-[18px]">
          <div className="mb-2 mt-4 font-mono text-[10px] tracking-[0.1em] text-faint">
            最近轨迹
          </div>
          {(logs ?? []).map((l, i) => (
            <div
              key={i}
              className="flex items-baseline gap-2 rounded-md px-1.5 py-1 font-mono text-[11px] hover:bg-muted/30"
            >
              <span className="shrink-0 text-faint">
                {l.ts?.slice(11, 19)}
              </span>
              <span
                className={TYPE_COLOR[l.type] ?? "text-muted-foreground"}
              >
                {l.type}
              </span>
              <span className="min-w-0 truncate text-muted-foreground">
                {l.preview ?? ""}
              </span>
            </div>
          ))}
          {(logs ?? []).length === 0 && (
            <div className="px-1.5 py-1 text-[11px] text-faint">（空）</div>
          )}

          <div className="mb-2 mt-5 font-mono text-[10px] tracking-[0.1em] text-faint">
            在途任务 · {inFlight.length}
          </div>
          {inFlight.length === 0 && (
            <div className="px-1.5 py-1 text-[11px] text-faint">（无在途）</div>
          )}
          {inFlight.map((t) => (
            <div
              key={t.task_id}
              className="flex items-center gap-2.5 border-b border-line/40 px-1.5 py-2 text-[12px]"
            >
              <span className="shrink-0 font-mono text-[10px] text-faint">
                {t.task_id.slice(0, 8)}
              </span>
              <span
                className={`shrink-0 rounded-full px-1.5 font-mono text-[10px] ${
                  t.status === "awaiting_approval"
                    ? "bg-resin/12 text-resin"
                    : "bg-primary/10 text-primary"
                }`}
              >
                {t.status === "awaiting_approval"
                  ? "待批"
                  : t.status === "queued"
                    ? "排队"
                    : "执行中"}
              </span>
              <span className="min-w-0 truncate text-foreground/85">
                {(t.content ?? "").slice(0, 60)}
              </span>
            </div>
          ))}

          <div className="mb-2 mt-5 font-mono text-[10px] tracking-[0.1em] text-faint">
            记忆 · {(memories ?? []).length} 条
          </div>
          <div className="px-1.5 text-[11px] text-muted-foreground">
            全文检索与生命周期管理在「记忆」页。
          </div>
        </div>
      </div>
    </>
  );
}
