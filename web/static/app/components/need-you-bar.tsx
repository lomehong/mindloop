// 「需要你」全局行动条（theater.md §5）：任何落地页顶部常驻——
// 待审批 > 0 / 预算 ≥ 80% / 熔断 / stalled 四类信号聚合，点击直达。
// 数据与工作台 attention 区同源同 query key（命中缓存不重复请求）。

import { useQueries, useQuery } from "@tanstack/react-query";
import { Link } from "react-router";

import { fetchActivity, fetchApprovals, fetchIdentities, fetchUsage } from "~/lib/api";
import { signalsFor } from "~/lib/need-you";
import { STATUS_BACKGROUND_POLL_MS } from "~/lib/polling";

/** NeedYouBar 是全局注意力一处呈现：有信号时才渲染（零信号不出
 * 现——"没有需要你的事"的安心态由工作台承担，落地页保持安静）。 */
export function NeedYouBar({ identityId }: { identityId: string }) {
  const { data: identities } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });
  const list = identities ?? [];
  const approvalQueries = useQueries({
    queries: list.map((it) => ({
      queryKey: ["approvals", it.id],
      queryFn: () => fetchApprovals(it.id),
      refetchInterval: STATUS_BACKGROUND_POLL_MS,
    })),
  });
  const usageQueries = useQueries({
    queries: list.map((it) => ({
      queryKey: ["usage", it.id],
      queryFn: () => fetchUsage(it.id),
      refetchInterval: STATUS_BACKGROUND_POLL_MS,
    })),
  });
  const activityQueries = useQueries({
    queries: list.map((it) => ({
      queryKey: ["activity", it.id],
      queryFn: () => fetchActivity(it.id),
      refetchInterval: STATUS_BACKGROUND_POLL_MS,
    })),
  });

  const signals = list.flatMap((identity, i) =>
    signalsFor(
      identity,
      usageQueries[i]?.data,
      approvalQueries[i]?.data,
      activityQueries[i]?.data
    )
  );
  if (signals.length === 0) return null;

  // 本身份的信号置顶（操作员大概率正在看它），其余按原序。
  const ordered = [
    ...signals.filter((s) => s.name === identityId),
    ...signals.filter((s) => s.name !== identityId),
  ];
  const top = ordered[0];
  void top;

  return (
    <div
      data-testid="need-you-bar"
      className="mb-3 flex flex-wrap items-center gap-2 rounded-xl border border-resin/40 bg-resin/8 px-4 py-2"
    >
      <span className="size-1.5 shrink-0 rounded-full bg-resin" />
      <span className="font-mono text-[10.5px] tracking-[0.1em] text-resin">
        需要你 · {signals.length}
      </span>
      {ordered.slice(0, 3).map((s) => (
        <Link
          key={s.key}
          to={s.to}
          className="rounded-md border border-border bg-card px-2 py-0.5 text-[11.5px] text-foreground/85 transition-colors hover:text-foreground"
        >
          {s.name === identityId ? "" : `${s.name} · `}
          {s.text}
        </Link>
      ))}
      {ordered.length > 3 && (
        <span className="text-[11px] text-muted-foreground">
          还有 {ordered.length - 3} 项（工作台查看）
        </span>
      )}
    </div>
  );
}
