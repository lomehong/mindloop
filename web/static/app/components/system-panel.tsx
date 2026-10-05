import { useQuery } from "@tanstack/react-query";
import { Activity, ChevronDown } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { useControlsEnabled } from "~/components/thinker-controls";
import { Button } from "~/components/ui/button";
import { fetchConfig, fetchIdentities, fetchLlmHealth, probeLlm } from "~/lib/api";
import type { LlmProbeResult } from "~/lib/types";

// Trellis 系统面板：首页右侧的圆角卡（与身份档案卡同位、同构），
// 把"整台机器"的事实读数收进首页三栏——LLM 健康、工作区规模、版本。
// LLM 供应商分区的「详情」展开承接原导航栏健康气泡的全部内容：
// 失败详情/充值提示/思考节奏/即时探测（导航栏不再挂 llm 气泡）。

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

/** 最近一次真实调用的失败抬头（bin/llm 的分类标记翻成人话）。 */
const LAST_CALL_LABEL: Record<string, string> = {
  credit: "余额耗尽",
  auth: "密钥被拒",
  rate: "请求被限流",
  other: "上次调用失败",
};
const LAST_CALL_HINT: Record<string, string> = {
  openrouter: "https://openrouter.ai/settings/credits",
  anthropic: "https://console.anthropic.com/settings/billing",
  openai: "https://platform.openai.com/settings/organization/billing",
};

export function SystemPanel() {
  const controlsEnabled = useControlsEnabled();
  const [details, setDetails] = useState(false);
  const [probing, setProbing] = useState(false);
  const [probe, setProbe] = useState<LlmProbeResult | null>(null);

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
  const last = health?.last_call ?? null;
  const hardFail =
    !!last && !last.ok && (last.kind === "credit" || last.kind === "auth");
  const failLabel =
    last && !last.ok ? LAST_CALL_LABEL[last.kind ?? "other"] : null;

  const runProbe = async () => {
    setProbing(true);
    setProbe(null);
    try {
      setProbe(await probeLlm());
    } catch (error) {
      toast.error((error as Error).message);
    } finally {
      setProbing(false);
    }
  };

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
              {hardFail && failLabel && (
                <div className="py-0.5 text-[11px] font-semibold text-clay">
                  最近调用失败：{failLabel}
                </div>
              )}
              {health.identities.slice(0, 4).map((it) => (
                <div key={it.id} className="flex items-center gap-2 py-0.5">
                  <span className="truncate font-mono text-[12px]">{it.name}</span>
                  <span className="ml-auto text-[11px] text-muted-foreground">
                    {it.failures_1h > 0 ? `${it.failures_1h} 失败/1h` : "—"}
                  </span>
                </div>
              ))}
              <button
                type="button"
                aria-expanded={details}
                className="mt-1 flex w-full items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
                onClick={() => setDetails(!details)}
              >
                <ChevronDown className={`size-3 transition-transform ${details ? "rotate-180" : ""}`} />
                详情
              </button>
              {details && (
                <div className="mt-1.5 space-y-2 border-t border-border pt-2">
                  {last && !last.ok && (
                    <div
                      className={`rounded border p-2 ${
                        hardFail ? "border-clay/40 bg-clay/10" : "border-resin/40 bg-resin/10"
                      }`}
                    >
                      <div className="text-[12px] font-semibold">
                        最近一次调用失败：{failLabel}
                        {last.http_code ? `（HTTP ${last.http_code}）` : ""}
                      </div>
                      {last.message && (
                        <div className="mt-1 break-words text-[11px] text-muted-foreground">{last.message}</div>
                      )}
                      {last.kind === "credit" && (
                        <div className="mt-1 text-[11px]">
                          心智仍在持续尝试，但在密钥充值之前，每次思考都会失败。
                          {last.provider && LAST_CALL_HINT[last.provider] && (
                            <>
                              前往{" "}
                              <a className="underline" href={LAST_CALL_HINT[last.provider]} target="_blank" rel="noreferrer">
                                {LAST_CALL_HINT[last.provider]}
                              </a>
                              {" "}充值，或在服务根目录的 .env 里更新 key 后重启心智。
                            </>
                          )}
                        </div>
                      )}
                      {last.kind === "auth" && (
                        <div className="mt-1 text-[11px]">请在服务根目录的 .env 里填入有效的 key，然后重启心智。</div>
                      )}
                      {last.ts && <div className="mt-1 text-[10px] text-muted-foreground">最近尝试 {last.ts}</div>}
                    </div>
                  )}
                  {health.identities.map((it) =>
                    it.cadence || it.last_failure ? (
                      <div key={it.id} className="text-[11px]">
                        <span className="font-mono">{it.name}</span>
                        {it.cadence && (
                          <span className="text-muted-foreground">
                            ：思考节奏中位 {it.cadence.recent_median_s}s
                            {it.cadence.baseline_median_s
                              ? `（基线 ${it.cadence.baseline_median_s}s）`
                              : ""}
                          </span>
                        )}
                        {it.last_failure && (
                          <div className="text-[10px] text-muted-foreground">
                            最近：{it.last_failure.content}
                          </div>
                        )}
                      </div>
                    ) : null
                  )}
                  {controlsEnabled && (
                    <div className="space-y-1">
                      <Button
                        variant="outline"
                        size="sm"
                        className="w-full"
                        disabled={probing}
                        title="发起一次真实的 LLM 调用（成本几分钱）"
                        onClick={runProbe}
                      >
                        <Activity className={`size-3 ${probing ? "animate-pulse" : ""}`} />
                        {probing ? "探测中…" : "立即探测供应商"}
                      </Button>
                      {probe && (
                        <div className="font-mono text-[11px]">
                          {probe.ok
                            ? `成功 · ${probe.latency_ms}ms${probe.provider ? ` · 经由 ${probe.provider}` : ""}`
                            : `失败 · ${probe.error}`}
                        </div>
                      )}
                    </div>
                  )}
                </div>
              )}
            </>
          ) : (
            <div className="py-1 text-muted-foreground">—</div>
          )}
        </div>
      </section>
    </aside>
  );
}
