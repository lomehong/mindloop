import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";

import { fetchConfig, fetchIdentities, fetchLlmHealth } from "~/lib/api";

// Trellis 系统面板：首页右侧的圆角卡（与身份档案卡同位、同构），
// 把"整台机器"的事实读数收进首页三栏——LLM 健康、工作区规模、版本。

function silent<T>(fn: () => Promise<T>): () => Promise<T | null> {
  return async () => {
    try {
      return await fn();
    } catch {
      return null;
    }
  };
}

const HEALTH_DOT: Record<string, string> = {
  ok: "bg-primary",
  degraded: "bg-resin",
  erroring: "bg-clay",
  unknown: "bg-muted-foreground/40",
};

export function SystemPanel() {
  const { data: health } = useQuery({
    queryKey: ["panel-llm-health"],
    queryFn: silent(fetchLlmHealth),
    refetchInterval: 30_000,
  });
  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    refetchInterval: 30_000,
  });
  const { data: config } = useQuery({
    queryKey: ["panel-config"],
    queryFn: silent(fetchConfig),
    staleTime: Infinity,
  });

  const live = identities?.filter((it) => it.live).length ?? 0;
  const total = identities?.length ?? 0;

  return (
    <aside className="hidden w-[276px] shrink-0 flex-col gap-3 overflow-y-auto rounded-2xl border border-border bg-card p-3 xl:flex">
      <div className="rounded-xl bg-muted p-3">
        <div className="text-[13.5px] font-semibold">系统</div>
        <div className="text-[11px] text-muted-foreground">
          {config?.version ? `v${config.version}` : "dev"}
          {config?.git_commit && (
            <span className="ml-1 font-mono">{config.git_commit.slice(0, 7)}</span>
          )}
        </div>
      </div>

      <section>
        <div className="px-1 pb-1 text-[11px] tracking-[0.08em] text-muted-foreground/70">
          工作区
        </div>
        <div className="rounded-xl border border-border px-3 py-2 text-[12.5px]">
          <div className="flex justify-between py-0.5">
            <span className="text-muted-foreground">身份</span>
            <span className="font-mono">{total}</span>
          </div>
          <div className="flex justify-between py-0.5">
            <span className="text-muted-foreground">心智运行中</span>
            <span className="font-mono">{live}</span>
          </div>
        </div>
      </section>

      <section>
        <div className="px-1 pb-1 text-[11px] tracking-[0.08em] text-muted-foreground/70">
          LLM 供应商
        </div>
        <div className="rounded-xl border border-border px-3 py-2 text-[12.5px]">
          {health ? (
            <>
              <div className="flex items-center gap-2 py-0.5">
                <span className={`h-1.5 w-1.5 rounded-full ${HEALTH_DOT[health.status] ?? "bg-muted-foreground/40"}`} />
                <span className="font-mono">{health.status}</span>
                <span className="ml-auto text-[11px] text-muted-foreground">
                  {health.failures_1h > 0 ? `${health.failures_1h} 失败/1h` : "无失败"}
                </span>
              </div>
              {health.identities.slice(0, 4).map((it) => (
                <div key={it.id} className="flex items-center gap-2 py-0.5">
                  <span className="truncate font-mono text-[12px]">{it.name}</span>
                  <span className="ml-auto text-[11px] text-muted-foreground">
                    {it.failures_1h > 0 ? `${it.failures_1h} 失败/1h` : "—"}
                  </span>
                </div>
              ))}
              <Link
                to="/"
                className="mt-1 block text-[11px] text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
              >
                打开健康气泡查看详情（导航栏 llm）
              </Link>
            </>
          ) : (
            <div className="py-1 text-muted-foreground">—</div>
          )}
        </div>
      </section>
    </aside>
  );
}
