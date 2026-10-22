// 更新横幅（检测升级的界面出口）：后台检测由 Go 侧 Checker 周期执行
//（MINDLOOP_UPDATE_INTERVAL，缺省 6h），这里只消费读数——有新版才
// 挂横幅，只提示不自动安装；「升级」按钮是安装的唯一入口，点击即
// 操作员显式授权（internal/update 三铁律之二）。检测失败静默——
// 横幅不打扰，手动检查在左下角构建戳菜单。

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";

import { Button } from "~/components/ui/button";
import { fetchUpdateLatest, installUpdate } from "~/lib/api";

const POLL_MS = 15 * 60_000; // 前端轮询只是读缓存；真检测节奏在 Go 侧

export function UpdateBanner() {
  const queryClient = useQueryClient();
  const { data: info } = useQuery({
    queryKey: ["update-latest"],
    queryFn: fetchUpdateLatest,
    refetchInterval: POLL_MS,
    staleTime: 10 * 60_000,
    retry: false,
  });
  const install = useMutation({
    mutationFn: installUpdate,
    onSuccess: (result) => {
      // 成功后失效读数：binary 模式重启前仍会显示旧读数，但升级动作
      // 已完成——toast 把生效方式说清楚。
      void queryClient.invalidateQueries({ queryKey: ["update-latest"] });
      if (result.mode === "installer") {
        toast.success(result.detail, { duration: 12_000 });
      } else if (result.mode === "binary") {
        toast.success(result.detail, { duration: 12_000 });
      } else {
        toast.info(result.detail);
      }
    },
    onError: (error: Error) => toast.error(error.message),
  });

  if (!info?.has_update) return null;
  return (
    <section className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-xl border border-resin/45 bg-resin/5 px-4 py-2.5">
      <span className="inline-flex shrink-0 items-center gap-1.5 rounded-full border border-resin/50 px-2.5 py-0.5 font-mono text-[11px] text-resin">
        <span className="size-[5px] animate-pulse rounded-full bg-resin" />
        新版本 {info.latest_tag}
      </span>
      <span className="min-w-0 flex-1 truncate text-[12.5px] text-muted-foreground">
        当前 {info.current}
        {info.notes ? ` · ${info.notes}` : ""}
      </span>
      {info.url && (
        <a
          href={info.url}
          target="_blank"
          rel="noreferrer"
          className="shrink-0 font-mono text-[11px] text-muted-foreground hover:text-foreground"
        >
          Release Notes ↗
        </a>
      )}
      <Button
        size="sm"
        className="shrink-0"
        disabled={install.isPending}
        onClick={() => install.mutate()}
        title="升级是操作员特权动作：点击即授权（桌面壳走安装器；二进制就地替换后需重启）"
      >
        {install.isPending ? "升级中…" : "去升级 →"}
      </Button>
    </section>
  );
}
