import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ear, Eye, Hand, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "~/components/confirm-dialog";
import { IdentityTabs } from "~/components/identity-tabs";
import { Badge } from "~/components/ui/badge";
import { Button } from "~/components/ui/button";
import { Checkbox } from "~/components/ui/checkbox";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "~/components/ui/empty";
import { Input } from "~/components/ui/input";
import { LoadingDots } from "~/components/ui/loading-dots";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import {
  addSensor,
  fetchIdentityStatus,
  fetchSensors,
  fetchTaste,
  removeSensor,
  toggleSensor,
} from "~/lib/api";
import { STATUS_BACKGROUND_POLL_MS } from "~/lib/polling";
import type { SensorView } from "~/lib/types";
import { cn } from "~/lib/utils";

export function meta() {
  return [{ title: "mindloop · 感知" }];
}

/** 类型徽标配色。 */
function typeVariant(
  type: string
): "default" | "secondary" | "outline" {
  switch (type) {
    case "file":
      return "default";
    case "git":
      return "secondary";
    case "web":
      return "outline";
    default:
      return "secondary";
  }
}

/** 感官卡片的公共渲染（列表 + 启停 + 移除）。 */
function SensorCard({
  sensor,
  onToggle,
  onRemove,
  pending,
}: {
  sensor: SensorView;
  onToggle: (id: string, enabled: boolean) => void;
  onRemove: (s: SensorView) => void;
  pending: boolean;
}) {
  const enabled = sensor.enabled !== false;
  const target = sensor.path || sensor.url || "";
  const rules = sensor.salience?.rules ?? [];
  const learningOff = (sensor.learning_days ?? 0) < 0;
  return (
    <div
      className={cn("rounded-lg border p-3", !enabled && "opacity-60")}
    >
      <div className="flex flex-wrap items-center gap-2">
        <Checkbox
          checked={enabled}
          disabled={pending}
          aria-label={`${enabled ? "停用" : "启用"} ${sensor.id}`}
          onCheckedChange={(checked) =>
            onToggle(sensor.id, checked === true)
          }
        />
        <span className="font-mono text-sm font-medium">{sensor.id}</span>
        <Badge variant={typeVariant(sensor.type)}>{sensor.type}</Badge>
        {!enabled && <Badge variant="outline">已停用</Badge>}
        {!learningOff && <Badge variant="outline">观察期</Badge>}
        <span className="ml-auto" />
        <Button
          variant="ghost"
          size="sm"
          disabled={pending}
          onClick={() => onRemove(sensor)}
        >
          <Trash2 className="size-3" />
          移除
        </Button>
      </div>
      <div className="mt-1 break-all font-mono text-xs text-muted-foreground">
        {target}
      </div>
      {(sensor.keywords?.length ?? 0) > 0 && (
        <div className="mt-1 text-xs text-muted-foreground">
          议程关键词：{sensor.keywords?.join("、")}
        </div>
      )}
      {rules.length > 0 && (
        <div className="mt-1 text-xs text-muted-foreground">
          规则：
          {rules
            .map(
              (r) => `${r.name}→${r.salience}${r.min ? `(≥${r.min})` : ""}`
            )
            .join("；")}
        </div>
      )}
    </div>
  );
}

function SectionHead({
  icon,
  title,
  hint,
}: {
  icon: React.ReactNode;
  title: string;
  hint: string;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <span className="flex items-center gap-1.5 font-medium">{icon}{title}</span>
      <span className="text-[11px] text-muted-foreground">{hint}</span>
    </div>
  );
}

/** 感知页：五感的统一管理面（docs/designs/perception.md）。
 * 眼=状态观察（file/git/web），耳=消息流（webhook/渠道降档），
 * 鼻=异常反射（自动机制+内感受），舌=味觉证据（归因反馈），
 * 身=触达（robotd，按设计节奏后置）。 */
export default function SensorsPage() {
  const { identityId = "" } = useParams();
  const queryClient = useQueryClient();
  const [sensorToRemove, setSensorToRemove] = useState<SensorView | null>(
    null
  );
  const [newType, setNewType] = useState("file");
  const [newTarget, setNewTarget] = useState("");
  const [newKeywords, setNewKeywords] = useState("");
  const [newWakeWords, setNewWakeWords] = useState("");

  const { data: status } = useQuery({
    queryKey: ["status", identityId],
    queryFn: () => fetchIdentityStatus(identityId),
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });

  const { data: view, isLoading } = useQuery({
    queryKey: ["sensors", identityId],
    queryFn: () => fetchSensors(identityId),
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });

  const { data: taste } = useQuery({
    queryKey: ["taste", identityId],
    queryFn: () => fetchTaste(identityId),
    refetchInterval: STATUS_BACKGROUND_POLL_MS,
  });

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ["sensors", identityId] });
    queryClient.invalidateQueries({ queryKey: ["taste", identityId] });
  };

  const toggle = useMutation({
    mutationFn: ({ id, enabled }: { id: string; enabled: boolean }) =>
      toggleSensor(identityId, id, enabled),
    onSuccess: (result) => {
      toast.success(result.enabled ? `已启用 ${result.id}` : `已停用 ${result.id}`);
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const remove = useMutation({
    mutationFn: (id: string) => removeSensor(identityId, id),
    onSuccess: (result) => {
      toast.success(`已移除 ${result.removed}`);
      setSensorToRemove(null);
      invalidate();
    },
    onError: (error: Error) => toast.error(error.message),
  });

  const add = useMutation({
    mutationFn: () => {
      const body: Partial<SensorView> & { type: string } = {
        type: newType,
      };
      if (newType === "web" || newType === "webhook") body.url = newTarget;
      else body.path = newTarget;
      const keywords = newKeywords
        .split(",")
        .map((k) => k.trim())
        .filter(Boolean);
      if (keywords.length) body.keywords = keywords;
      const wakeWords = newWakeWords
        .split(",")
        .map((k) => k.trim())
        .filter(Boolean);
      if (wakeWords.length) {
        body.salience = {
          rules: [{ name: "wake", keywords: wakeWords, salience: "s2" }],
        };
      }
      return addSensor(identityId, body);
    },
    onSuccess: (result: { added: string; secret?: string }) => {
      toast.success(
        `已接入 ${result.added}——学习期 3 天内只沉淀不叫醒`
      );
      if (result.secret) {
        toast.info(`webhook 密钥（只显示这一次）：${result.secret}`);
      }
      setNewTarget("");
      setNewKeywords("");
      setNewWakeWords("");
      invalidate();
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

  const sensors = view.sensors ?? [];
  const eyes = sensors.filter(
    (s) => s.type === "file" || s.type === "git" || s.type === "web"
  );
  const ears = sensors.filter((s) => s.type === "webhook");
  const selfs = sensors.filter((s) => s.type === "self");
  const commonToggle = (id: string, enabled: boolean) =>
    toggle.mutate({ id, enabled });
  const commonRemove = (s: SensorView) => setSensorToRemove(s);

  return (
    <div className="mx-auto w-full max-w-4xl">
      <IdentityTabs
        identityId={identityId}
        live={status?.live ?? false}
        active="sensors"
      />
      <div className="mx-auto w-full max-w-4xl space-y-6 pb-10">
        <div className="flex items-center gap-3">
          <h2 className="font-mono text-xs font-medium uppercase tracking-wider text-muted-foreground">
            感知（五感 · sensors.json）
          </h2>
          <span className="text-[11px] text-muted-foreground">
            眼睛里有事：观察到的变化按显著性分级——多数安静沉淀，命中
            规则的才叫醒。改这里即刻生效。
          </span>
          <Button
            variant="outline"
            size="sm"
            className="ml-auto"
            onClick={invalidate}
          >
            <RefreshCw className="size-3" />
            刷新
          </Button>
        </div>

        {/* ── 眼 · 状态观察 ── */}
        <div className="space-y-3">
          <SectionHead
            icon={<Eye className="size-4" />}
            title="眼 · 观察"
            hint="盯住目录、仓库、网页——变化按显著性分级沉淀或叫醒"
          />
          <div className="rounded-lg border p-3">
            <div className="flex flex-wrap items-center gap-2">
              <Select value={newType} onValueChange={(v) => setNewType(v)}>
                <SelectTrigger className="w-[92px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="file">文件目录</SelectItem>
                  <SelectItem value="git">git 仓库</SelectItem>
                  <SelectItem value="web">网页/RSS</SelectItem>
                  <SelectItem value="webhook">webhook（耳）</SelectItem>
                </SelectContent>
              </Select>
              <Input
                className="w-64"
                placeholder={
                  newType === "web" || newType === "webhook"
                    ? "https://example.com"
                    : "要观察的目录"
                }
                value={newTarget}
                onChange={(e) => setNewTarget(e.target.value)}
              />
              <Input
                className="w-44"
                placeholder="关键词（逗号分隔，可空）"
                value={newKeywords}
                onChange={(e) => setNewKeywords(e.target.value)}
              />
              <Input
                className="w-52"
                placeholder="叫醒关键词（可空）"
                value={newWakeWords}
                onChange={(e) => setNewWakeWords(e.target.value)}
              />
              <Button
                size="sm"
                disabled={!newTarget.trim() || add.isPending}
                onClick={() => add.mutate()}
              >
                <Plus className="size-3" />
                接入
              </Button>
            </div>
            <div className="mt-2 text-[11px] text-muted-foreground">
              新感官有 3 天观察期（只沉淀不叫醒）；"叫醒关键词"命中的
              变化会叫醒心智。
            </div>
          </div>
          {eyes.length === 0 ? (
            <Empty>
              <EmptyHeader>
                <EmptyMedia variant="icon">
                  <Eye className="size-5" />
                </EmptyMedia>
                <EmptyTitle className="text-base">
                  眼睛还是闭着的
                </EmptyTitle>
                <EmptyDescription>
                  用上面的表单接入第一个观察目标——比如这个项目的文档
                  目录，或一个你每天看的页面。
                </EmptyDescription>
              </EmptyHeader>
            </Empty>
          ) : (
            eyes.map((sensor) => (
              <SensorCard
                key={sensor.id}
                sensor={sensor}
                onToggle={commonToggle}
                onRemove={commonRemove}
                pending={toggle.isPending || remove.isPending}
              />
            ))
          )}
        </div>

        {/* ── 耳 · 消息流 ── */}
        <div className="space-y-3">
          <SectionHead
            icon={<Ear className="size-4" />}
            title="耳 · 听闻"
            hint="外部系统说话，它在线听——webhook 推送即事件"
          />
          {ears.length === 0 ? (
            <div className="rounded-lg border border-dashed p-3 text-xs text-muted-foreground">
              未配置。用上方表单选 "webhook（耳）" 接入——生成 HMAC
              密钥后，外部系统 POST 到
              <code className="mx-1 rounded bg-muted px-1">
                /hook/{identityId}/&lt;感官id&gt;
              </code>
              即成为它的耳朵。
            </div>
          ) : (
            ears.map((sensor) => (
              <SensorCard
                key={sensor.id}
                sensor={sensor}
                onToggle={commonToggle}
                onRemove={commonRemove}
                pending={toggle.isPending || remove.isPending}
              />
            ))
          )}
        </div>

        {/* ── 鼻 · 嗅探 ── */}
        <div className="space-y-3">
          <SectionHead
            icon={<span className="text-sm">👃</span>}
            title="鼻 · 嗅探"
            hint="自动机制：速率突增 / 首次出现 / 阈值越界 / 缺席 / 内感受"
          />
          <div className="rounded-lg border border-dashed p-3 text-xs text-muted-foreground">
            常驻自动运行，无需配置：事件流的模式偏移会以
            <code className="mx-1 rounded bg-muted px-1">anomaly</code>
            事件留痕并按显著性叫醒；配置了
            <code className="mx-1 rounded bg-muted px-1">expect_every</code>
            的感官"该来的没来"会被报告；内感受（预算水位 / 模型熔断）
            直达通知。
            {selfs.length > 0 && (
              <span className="ml-1 text-foreground">
                内感受已开启：{selfs.map((s) => s.id).join("、")}。
              </span>
            )}
          </div>
        </div>

        {/* ── 舌 · 味觉 ── */}
        <div className="space-y-3">
          <SectionHead
            icon={<span className="text-sm">👅</span>}
            title="舌 · 品评"
            hint="你的归因反馈是它校准的证据面（近 7 天）"
          />
          <div className="rounded-lg border p-3 text-xs">
            {taste ? (
              (taste.Undoes ?? 0) + (taste.Approves ?? 0) + (taste.Denies ?? 0) + (taste.MissedReports ?? 0) === 0 ? (
                <span className="text-muted-foreground">
                  窗口内还没有味觉信号——撤销时用
                  <code className="mx-1 rounded bg-muted px-1">undo --because 提案多余</code>
                  类归因、审批决议都会成为它的校准证据。
                </span>
              ) : (
                <span>
                  撤销 {taste.Undoes ?? 0}（提案多余{" "}
                  {taste.UndoThresholdTight ?? 0}）｜批准{" "}
                  {taste.Approves ?? 0}｜拒绝 {taste.Denies ?? 0}
                  {(taste.MissedReports ?? 0) > 0 && (
                    <span className="text-amber-600 dark:text-amber-400">
                      {" "}
                      ｜⚠ 漏报匹配 {taste.MissedReports} 次
                      （{taste.MissedSubjects?.join("、")}）
                    </span>
                  )}
                </span>
              )
            ) : (
              <LoadingDots />
            )}
          </div>
        </div>

        {/* ── 身 · 触达 ── */}
        <div className="space-y-3">
          <SectionHead
            icon={<Hand className="size-4" />}
            title="身 · 触达"
            hint="动作闭环（robotd MCP 服务器）——动作必须自带回读验证"
          />
          {view?.robotd?.configured ? (
            <div className="rounded-lg border p-3 text-xs space-y-1">
              <div>
                已接入（mcp.json）。模式：
                <span className={view.robotd.mode === "action" ? "text-emerald-600 dark:text-emerald-400" : "text-amber-600 dark:text-amber-400"}>
                  {view.robotd.mode === "action" ? "动作已授权" : "观察模式（只许看不许动）"}
                </span>
              </div>
              {view.robotd.mode === "action" && (
                <div className="text-muted-foreground">
                  窗口白名单：{view.robotd.allow?.join("、")}——动作只在这些标题的窗口上进行；密码/凭据画面在任何模式下都拒绝。
                </div>
              )}
              <div className="text-muted-foreground">
                心智用法：截屏 <code>look</code>（下一轮思考即所见）；动作{" "}
                <code>mcp call robotd screen_click / screen_type</code>（每次动作强制回读截图作为证据）。
              </div>
            </div>
          ) : (
            <div className="rounded-lg border border-dashed p-3 text-xs text-muted-foreground space-y-1">
              <div>
                未接入。接入命令（在项目目录执行，白名单按需给出；不给 = 只许看不许动）：
              </div>
              <div className="font-mono break-all bg-muted rounded px-2 py-1">
                mindloop mcp add robotd --identity {identityId} -- &lt;mindloop.exe 绝对路径&gt; robotd --window-allow 记事本
              </div>
              <div>
                安全链：窗口白名单谓词（动作级 tripwire）→ 敏感窗口默认拒绝 →
                每个动作强制回读验证，失败即报错 → 全程 stderr 审计留痕。
              </div>
            </div>
          )}
        </div>
      </div>

      <ConfirmDialog
        open={sensorToRemove !== null}
        onOpenChange={(open) => {
          if (!open) setSensorToRemove(null);
        }}
        title={`移除感官 ${sensorToRemove?.id ?? ""}？`}
        description={`将停止观察 ${sensorToRemove?.path || sensorToRemove?.url || ""}（已落轨迹的历史保留）。`}
        confirmText="移除"
        onConfirm={() => {
          if (sensorToRemove) remove.mutate(sensorToRemove.id);
        }}
      />
    </div>
  );
}
