import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Server } from "lucide-react";
import { useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "~/components/ui/empty";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { Skeleton } from "~/components/ui/loading-skeleton";
import {
  fetchConnections,
  updateWecomChannel,
} from "~/lib/api";
import { STATUS_BACKGROUND_POLL_MS } from "~/lib/polling";

export function meta() {
  return [{ title: "mindloop · 连接" }];
}

// source 徽标文案（与技能页同一口径）。
const SOURCE_LABEL: Record<string, string> = {
  identity: "身份级",
  global: "全局",
};

/** 归属/元信息胶囊（mockup .mbadge）。 */
const MBADGE =
  "rounded-full border border-line-strong px-1.5 py-px font-mono text-[9.5px] font-normal leading-[1.6] text-muted-foreground";
/** 状态胶囊（mockup .pill / .pill.on）。 */
const PILL =
  "inline-flex items-center gap-1.5 rounded-full border border-line-strong px-2 py-px font-mono text-[10.5px] text-muted-foreground";
const PILL_ON =
  "inline-flex items-center gap-1.5 rounded-full border border-primary/35 bg-primary/10 px-2 py-px font-mono text-[10.5px] text-primary";

/** 章标题（mockup .sec-label）。 */
function SecLabel({ children }: { children: React.ReactNode }) {
  return (
    <div className="mb-2 flex items-center gap-1.5 font-mono text-[10.5px] tracking-[0.14em] text-faint">
      {children}
    </div>
  );
}

export default function ConnectionsPage() {
  const { identityId = "" } = useParams();

  const {
    data: view,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
    queryKey: ["connections", identityId],
    queryFn: () => fetchConnections(identityId),
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });

  // 渠道配置表单态：只反映"正在编辑的草稿"，保存成功后由
  // invalidate 拉回服务端视图（凭据不回显，secret 输入框永远空）。
  const [botId, setBotId] = useState("");
  const [secret, setSecret] = useState("");
  const [allowText, setAllowText] = useState("");
  const queryClient = useQueryClient();
  const saveChannel = useMutation({
    mutationFn: () =>
      updateWecomChannel(identityId, {
        bot_id: botId.trim(),
        secret: secret.trim(),
        allow: allowText
          .split(/[,;，；]/)
          .map((item) => item.trim())
          .filter(Boolean),
      }),
    onSuccess: (result) => {
      toast.success(
        result.ready
          ? `已保存：${result.label} 配置就绪`
          : `已保存：${result.label}（配置未齐，bridge 无法启动）`
      );
      setSecret("");
      queryClient.invalidateQueries({ queryKey: ["connections", identityId] });
    },
    onError: (error: Error) => toast.error(error.message),
  });

  if (isError) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <div className="mx-auto w-full max-w-4xl pb-10">
          <QueryErrorBanner error={error} onRetry={() => void refetch()} />
        </div>
      </div>
    );
  }

  if (isLoading || !view) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <div className="mx-auto w-full max-w-4xl space-y-4 pb-10">
          <Skeleton className="h-7 w-44" />
          <Skeleton className="h-20 w-full rounded-xl" />
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-40 w-full rounded-xl" />
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
      <div className="mx-auto w-full max-w-4xl space-y-4 pb-10">
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="font-note text-[22px] font-semibold tracking-[0.01em]">
            外部连接
          </h1>
          <span className="text-[13px] text-muted-foreground">
            MCP 服务器 · 配置文件 · 外部渠道
          </span>
        </div>

        {view.mcp_config_error && (
          <div className="rounded-lg border border-clay/45 bg-clay/[0.06] px-3 py-2 text-xs text-clay">
            <div className="font-medium">
              mcp.json 解析失败——MCP 当前不生效。
            </div>
            <p className="mt-1">{view.mcp_config_error}</p>
          </div>
        )}

        <section>
          <SecLabel>MCP 服务器</SecLabel>
          {view.mcp_servers.length === 0 ? (
            <Empty>
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <Server className="size-5" />
                </EmptyMedia>
                <EmptyTitle className="text-base">
                  还没有 MCP 服务器
                </EmptyTitle>
                <EmptyDescription>
                  <div>
                    用 CLI 添加（--identity 写身份级，缺省写全局、对所有身份生效）：
                  </div>
                  <div className="mt-2 break-all rounded-md bg-muted px-3 py-2 text-left font-mono text-[11px]">
                    {`mindloop mcp add github --identity ${view.identity.id} -- npx -y @modelcontextprotocol/server-github`}
                  </div>
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            <div className="space-y-2.5">
              {view.mcp_servers.map((server) => (
                <div
                  key={server.name}
                  className="rounded-xl border border-line bg-card px-4 py-3"
                >
                  <div className="mb-1.5 flex flex-wrap items-center gap-2">
                    <span className="font-mono text-[13px] font-semibold">
                      {server.name}
                    </span>
                    <span className={MBADGE}>{server.transport}</span>
                    <span className={MBADGE}>
                      {SOURCE_LABEL[server.source] ?? server.source}
                    </span>
                  </div>
                  <div className="break-all font-mono text-[11.5px] leading-[1.75] text-muted-foreground">
                    <div>
                      {server.transport === "http"
                        ? server.url
                        : [server.command, ...(server.args ?? [])]
                            .filter(Boolean)
                            .join(" ")}
                    </div>
                    <div>
                      环境变量：{server.env_keys.join(", ") || "—"} · 请求头：
                      {server.header_keys.join(", ") || "—"}（值不回显）
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </section>

        <section>
          <SecLabel>配置文件</SecLabel>
          <div>
            {view.mcp_files.map((file) => (
              <div
                key={file.path}
                className="flex flex-wrap items-center gap-x-2.5 gap-y-1 border-t border-line px-0.5 py-2.5 first:border-t-0"
              >
                <span className={MBADGE}>{file.label}</span>
                <span className="min-w-0 flex-1 break-all font-mono text-[11px] text-muted-foreground">
                  {file.path}
                </span>
                <span className={file.exists ? PILL_ON : PILL}>
                  {file.exists ? "已创建" : "未创建"}
                </span>
              </div>
            ))}
          </div>
        </section>

        <section>
          <SecLabel>外部渠道（bridge）</SecLabel>
          {view.channels.map((channel) => (
            <ChannelCard
              key={channel.channel}
              channel={channel}
              botId={botId}
              setBotId={setBotId}
              secret={secret}
              setSecret={setSecret}
              allowText={allowText}
              setAllowText={setAllowText}
              onSave={() => saveChannel.mutate()}
              saving={saveChannel.isPending}
            />
          ))}
        </section>
      </div>
    </div>
  );
}

// ChannelCard 是单个渠道的配置卡：状态徽标 + 表单。secret 永不
// 回显（占位符只报"已配置/未配置"），留空提交 = 保持既有值。
function ChannelCard(props: {
  channel: {
    channel: string;
    label: string;
    bot_id: string;
    secret_set: boolean;
    allow: string[];
    ready: boolean;
    cursor_exists: boolean;
    note: string;
  };
  botId: string;
  setBotId: (value: string) => void;
  secret: string;
  setSecret: (value: string) => void;
  allowText: string;
  setAllowText: (value: string) => void;
  onSave: () => void;
  saving: boolean;
}) {
  const { channel } = props;
  return (
    <div className="rounded-xl border border-line bg-card px-4 pt-3.5 pb-3">
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <span className="text-[13.5px] font-semibold">{channel.label}</span>
        <span className={channel.ready ? PILL_ON : PILL}>
          {channel.ready ? "配置就绪" : "配置未齐"}
        </span>
        {channel.cursor_exists && <span className={PILL}>bridge 曾运行</span>}
        <span className="font-mono text-[10.5px] text-faint">
          {channel.channel}
        </span>
      </div>

      <div className="flex flex-col gap-2.5">
        <label className="grid grid-cols-1 items-center gap-x-3 gap-y-1 md:grid-cols-[200px_1fr]">
          <span className="font-mono text-[11.5px] text-muted-foreground">
            BotID（当前：{channel.bot_id || "未配置"}）
          </span>
          <Input
            value={props.botId}
            onChange={(event) => props.setBotId(event.target.value)}
            placeholder="ww1234567890"
            className="h-8 max-w-[260px] font-mono text-xs"
          />
        </label>
        <label className="grid grid-cols-1 items-center gap-x-3 gap-y-1 md:grid-cols-[200px_1fr]">
          <span className="font-mono text-[11.5px] text-muted-foreground">
            Secret（{channel.secret_set ? "已配置，留空保持不变" : "未配置"}
            ）
          </span>
          <Input
            type="password"
            value={props.secret}
            onChange={(event) => props.setSecret(event.target.value)}
            placeholder={channel.secret_set ? "••••••••" : "长连接专用 Secret"}
            className="h-8 max-w-[260px] font-mono text-xs"
          />
        </label>
        <label className="grid grid-cols-1 items-center gap-x-3 gap-y-1 md:grid-cols-[200px_1fr]">
          <span className="font-mono text-[11.5px] text-muted-foreground">
            白名单 userid（逗号分隔；当前：
            {channel.allow?.length > 0 ? channel.allow.join("、") : "无"}）
          </span>
          <Input
            value={props.allowText}
            onChange={(event) => props.setAllowText(event.target.value)}
            placeholder="zhangsan,lisi"
            className="h-8 max-w-[260px] font-mono text-xs"
          />
        </label>
      </div>

      <div className="flex flex-wrap items-center gap-3 pt-3">
        <Button
          type="button"
          size="sm"
          className="h-7 px-2.5 text-[12px]"
          onClick={props.onSave}
          disabled={props.saving}
        >
          {props.saving ? "保存中…" : "保存渠道配置"}
        </Button>
        <p className="text-[12px] text-muted-foreground">{channel.note}</p>
      </div>
    </div>
  );
}
