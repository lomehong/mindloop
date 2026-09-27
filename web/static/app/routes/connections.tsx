import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Server } from "lucide-react";
import { useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { IdentityTabs } from "~/components/identity-tabs";
import { Badge } from "~/components/ui/badge";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "~/components/ui/empty";
import { LoadingDots } from "~/components/ui/loading-dots";
import {
  fetchConnections,
  fetchIdentityStatus,
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

export default function ConnectionsPage() {
  const { identityId = "" } = useParams();

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });

  const { data: view, isLoading } = useQuery({
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

  if (isLoading || !view) {
    return (
      <div className="flex justify-center py-20">
        <LoadingDots />
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-7xl">
      <IdentityTabs
        identityId={identityId}
        live={status?.live ?? false}
        active="connections"
        name={view.identity?.name}
      />
      <div className="mx-auto w-full max-w-4xl space-y-6 pb-10">
        <div className="flex items-center gap-3">
          <h2 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
            外部连接
          </h2>
          <span className="text-[11px] text-muted-foreground">
            MCP 服务器（工具）来自 mcp.json；env/headers 只显示键名，值不回显。配置编辑走
            CLI（mindloop mcp add/remove）。
          </span>
        </div>

        {view.mcp_config_error && (
          <div className="rounded-lg border border-red-300 bg-red-50 px-3 py-2 text-xs text-red-900 dark:border-red-800 dark:bg-red-950 dark:text-red-200">
            <div className="font-medium">
              mcp.json 解析失败——MCP 当前不生效。
            </div>
            <p className="mt-1">{view.mcp_config_error}</p>
          </div>
        )}

        <section className="space-y-3">
          <h3 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
            MCP 服务器
          </h3>
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
            <div className="space-y-3">
              {view.mcp_servers.map((server) => (
                <div key={server.name} className="rounded-lg border p-3">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-mono text-sm font-medium">
                      {server.name}
                    </span>
                    <Badge variant="outline" className="text-[10px]">
                      {server.transport}
                    </Badge>
                    <Badge variant="outline" className="text-[10px]">
                      {SOURCE_LABEL[server.source] ?? server.source}
                    </Badge>
                  </div>
                  <p className="mt-1 break-all font-mono text-xs text-muted-foreground">
                    {server.transport === "http"
                      ? server.url
                      : [server.command, ...(server.args ?? [])]
                          .filter(Boolean)
                          .join(" ")}
                  </p>
                  {(server.env_keys.length > 0 ||
                    server.header_keys.length > 0) && (
                    <p className="mt-1 text-[11px] text-muted-foreground">
                      {server.env_keys.length > 0 && (
                        <span>环境变量：{server.env_keys.join(", ")}</span>
                      )}
                      {server.env_keys.length > 0 &&
                        server.header_keys.length > 0 && <span> · </span>}
                      {server.header_keys.length > 0 && (
                        <span>请求头：{server.header_keys.join(", ")}</span>
                      )}
                      <span className="text-muted-foreground/70">
                        （值不回显）
                      </span>
                    </p>
                  )}
                </div>
              ))}
            </div>
          )}
        </section>

        <section className="space-y-2">
          <h3 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
            配置文件
          </h3>
          <div className="space-y-2">
            {view.mcp_files.map((file) => (
              <div
                key={file.path}
                className="flex flex-wrap items-center gap-2 rounded-lg border px-3 py-2"
              >
                <Badge variant="outline" className="text-[10px]">
                  {file.label}
                </Badge>
                <span className="break-all font-mono text-[11px] text-muted-foreground">
                  {file.path}
                </span>
                <Badge
                  variant={file.exists ? "secondary" : "outline"}
                  className="ml-auto text-[10px]"
                >
                  {file.exists ? "已创建" : "未创建"}
                </Badge>
              </div>
            ))}
          </div>
        </section>

        <section className="space-y-2">
          <h3 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
            外部渠道（bridge）
          </h3>
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
    <div className="space-y-3 rounded-lg border p-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium">{channel.label}</span>
        <Badge
          variant={channel.ready ? "secondary" : "outline"}
          className="text-[10px]"
        >
          {channel.ready ? "配置就绪" : "配置未齐"}
        </Badge>
        {channel.cursor_exists && (
          <Badge variant="outline" className="text-[10px]">
            bridge 曾运行
          </Badge>
        )}
        <span className="font-mono text-[10px] text-muted-foreground">
          {channel.channel}
        </span>
      </div>

      <div className="grid gap-2 sm:grid-cols-3">
        <label className="space-y-1">
          <span className="text-[11px] text-muted-foreground">
            BotID（当前：{channel.bot_id || "未配置"}）
          </span>
          <input
            value={props.botId}
            onChange={(event) => props.setBotId(event.target.value)}
            placeholder="ww1234567890"
            className="w-full rounded-md border bg-transparent px-2 py-1.5 font-mono text-xs outline-none focus:ring-1 focus:ring-ring"
          />
        </label>
        <label className="space-y-1">
          <span className="text-[11px] text-muted-foreground">
            Secret（{channel.secret_set ? "已配置，留空保持不变" : "未配置"}
            ）
          </span>
          <input
            type="password"
            value={props.secret}
            onChange={(event) => props.setSecret(event.target.value)}
            placeholder={channel.secret_set ? "••••••••" : "长连接专用 Secret"}
            className="w-full rounded-md border bg-transparent px-2 py-1.5 font-mono text-xs outline-none focus:ring-1 focus:ring-ring"
          />
        </label>
        <label className="space-y-1">
          <span className="text-[11px] text-muted-foreground">
            白名单 userid（逗号分隔；当前：
            {channel.allow?.length > 0 ? channel.allow.join("、") : "无"}）
          </span>
          <input
            value={props.allowText}
            onChange={(event) => props.setAllowText(event.target.value)}
            placeholder="zhangsan,lisi"
            className="w-full rounded-md border bg-transparent px-2 py-1.5 font-mono text-xs outline-none focus:ring-1 focus:ring-ring"
          />
        </label>
      </div>

      <div className="flex items-center gap-3">
        <button
          type="button"
          onClick={props.onSave}
          disabled={props.saving}
          className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground disabled:opacity-50"
        >
          {props.saving ? "保存中…" : "保存渠道配置"}
        </button>
        <p className="text-[11px] text-muted-foreground">{channel.note}</p>
      </div>
    </div>
  );
}
