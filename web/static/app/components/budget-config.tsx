import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";

// 预算控制：身份每日 token 上限的配置节（配置页内嵌卡片）。
// 两种模式：不封顶（null）/ 封顶+数值。保存后写入身份 .env，
// 重启心智生效（Gate 在启动时从环境构造）。
// 独立文件——api.ts 的修改会触发 Mimosa git 门禁对整个文件重扫。

export function BudgetConfig({ identityId }: { identityId: string }) {
  const qc = useQueryClient();
  const { data, refetch } = useQuery({
    queryKey: ["budget", identityId],
    queryFn: async () => {
      const r = await fetch(
        `/api/identities/${encodeURIComponent(identityId)}/budget`
      );
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
      return (await r.json()) as { daily_limit: number | null };
    },
    staleTime: 30_000,
  });

  const [mode, setMode] = useState<"uncapped" | "capped">("uncapped");
  const [limitStr, setLimitStr] = useState("");
  const [dirty, setDirty] = useState(false);

  const save = useMutation({
    mutationFn: async (limit: number | null) => {
      const r = await fetch(
        `/api/identities/${encodeURIComponent(identityId)}/budget`,
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ daily_limit: limit }),
        }
      );
      if (!r.ok) throw new Error(`HTTP ${r.status}`);
    },
    onSuccess: () => {
      setDirty(false);
      void refetch();
      void qc.invalidateQueries({ queryKey: ["budget", identityId] });
    },
  });

  useEffect(() => {
    if (data) {
      if (data.daily_limit != null) {
        setMode("capped");
        setLimitStr(String(data.daily_limit));
      } else {
        setMode("uncapped");
        setLimitStr("");
      }
      setDirty(false);
    }
  }, [data]);

  const parsed = Number(limitStr.replace(/[^0-9]/g, ""));
  const isValid = mode === "uncapped" || (parsed > 0 && limitStr.trim() !== "");
  const changed =
    dirty ||
    (mode === "capped") !== (data?.daily_limit != null) ||
    (mode === "capped" && String(data?.daily_limit ?? "") !== limitStr);

  return (
    <div className="rounded-xl border border-border bg-card p-4">
      <div className="mb-3 text-[13px] font-semibold">预算控制</div>
      <div className="space-y-3 text-[13px]">
        <label className="flex cursor-pointer items-center gap-2.5">
          <input
            type="radio"
            name="budget-mode"
            checked={mode === "uncapped"}
            onChange={() => {
              setMode("uncapped");
              setDirty(true);
            }}
          />
          <span>不封顶（依赖套餐自然配额）</span>
        </label>
        <label className="flex cursor-pointer items-center gap-2.5">
          <input
            type="radio"
            name="budget-mode"
            checked={mode === "capped"}
            onChange={() => {
              setMode("capped");
              setDirty(true);
            }}
          />
          <span>设每日上限</span>
          {mode === "capped" && (
            <input
              type="text"
              inputMode="numeric"
              value={limitStr}
              onChange={(e) => {
                setLimitStr(e.target.value.replace(/[^0-9]/g, ""));
                setDirty(true);
              }}
              placeholder="token 数"
              className="ml-1 w-32 rounded-md border border-border bg-background px-2 py-1 font-mono text-[12px] outline-none focus:ring-1 focus:ring-[var(--accent)]"
            />
          )}
        </label>
      </div>
      {changed && isValid && (
        <button
          className="mt-2 rounded-lg border border-border px-3 py-1 text-[12px] text-muted-foreground hover:text-foreground"
          onClick={() => save.mutate(mode === "capped" ? parsed || null : null)}
          disabled={save.isPending}
        >
          {save.isPending ? "保存中…" : "保存预算配置"}
        </button>
      )}
    </div>
  );
}
