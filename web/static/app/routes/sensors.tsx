import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { useParams } from "react-router";
import { toast } from "sonner";

import { ConfirmDialog } from "~/components/confirm-dialog";
import { QueryErrorBanner } from "~/components/query-error-banner";
import { Readout, Ro } from "~/components/readout";
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
import { Skeleton } from "~/components/ui/loading-skeleton";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import {
  addSensor,
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

const TYPE_LABELS: Record<string, string> = {
  file: "文件目录",
  git: "git 仓库",
  web: "网页/RSS",
  webhook: "webhook",
  self: "内感受",
};

const MBADGE =
  "rounded-full border border-line-strong px-1.5 py-px font-mono text-[9.5px] text-muted-foreground";
const PILL_NEUTRAL =
  "rounded-full border border-line-strong px-2 py-px font-mono text-[10.5px] text-muted-foreground";
const PILL_WARN =
  "rounded-full border border-resin/40 bg-resin/[0.08] px-2 py-px font-mono text-[10.5px] text-resin";
const PILL_ON =
  "rounded-full border border-primary/35 bg-primary/[0.10] px-2 py-px font-mono text-[10.5px] text-primary";
const CODE =
  "mx-0.5 rounded bg-muted px-1 py-px font-mono text-[11px] text-foreground";
const HINT = "text-[12px] leading-relaxed text-muted-foreground";

/** 五感区块卡：色点 + mono 节标题（+ 注记）。 */
function SenseCard({
  dot,
  label,
  note,
  children,
}: {
  dot: string;
  label: string;
  note?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="mb-3.5 rounded-xl border border-line bg-card px-4 pb-3 pt-3.5">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <span className="flex items-center gap-1.5 font-mono text-[10.5px] tracking-[0.14em] text-muted-foreground">
          <span className={cn("size-[7px] rounded-full", dot)} />
          {label}
        </span>
        {note && (
          <span className="font-mono text-[10.5px] tracking-normal text-faint">
            {note}
          </span>
        )}
      </div>
      {children}
    </div>
  );
}

/** 感官行（d-row）：启停 + id + 类型徽标 + 状态 pill + 目标/规则细节行。 */
function SensorRow({
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
    <div className="rounded-lg px-1.5 py-2 hover:bg-secondary">
      <div className="flex flex-wrap items-center gap-2">
        <Checkbox
          checked={enabled}
          disabled={pending}
          aria-label={`${enabled ? "停用" : "启用"} ${sensor.id}`}
          onCheckedChange={(checked) => onToggle(sensor.id, checked === true)}
        />
        <span className="font-mono text-[12.5px]">{sensor.id}</span>
        <span className={MBADGE}>
          {TYPE_LABELS[sensor.type] ?? sensor.type}
        </span>
        {!enabled && <span className={PILL_NEUTRAL}>已停用</span>}
        {!learningOff && <span className={PILL_WARN}>观察期</span>}
        <span className="flex-1" />
        <Button
          variant="ghost"
          size="sm"
          className="h-6 px-2 text-[11px] text-muted-foreground hover:text-clay"
          disabled={pending}
          aria-label={`移除 ${sensor.id}`}
          onClick={() => onRemove(sensor)}
        >
          <Trash2 className="size-3" />
          移除
        </Button>
      </div>
      <div className={cn("mt-1 space-y-0.5 pl-[26px]", !enabled && "opacity-50")}>
        {target && (
          <div className="break-all font-mono text-[11.5px] text-muted-foreground">
            {target}
          </div>
        )}
        {((sensor.keywords?.length ?? 0) > 0 || rules.length > 0) && (
          <div className="text-[12px] text-muted-foreground">
            {(sensor.keywords?.length ?? 0) > 0 &&
              `议程关键词：${sensor.keywords?.join("、")}`}
            {rules.length > 0 && (
              <>
                {(sensor.keywords?.length ?? 0) > 0 && " · "}
                规则{" "}
                {rules
                  .map(
                    (r) => `${r.name}→${r.salience}${r.min ? `(≥${r.min})` : ""}`
                  )
                  .join("；")}
              </>
            )}
          </div>
        )}
      </div>
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

  const {
    data: view,
    isLoading,
    isError,
    error,
    refetch,
  } = useQuery({
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

  if (isError) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <QueryErrorBanner error={error} onRetry={() => void refetch()} />
      </div>
    );
  }

  if (isLoading || !view) {
    return (
      <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
        <div className="mx-auto w-full max-w-4xl space-y-3.5 pb-10">
          <Skeleton className="h-7 w-56" />
          <Skeleton className="h-40 w-full rounded-xl" />
          <Skeleton className="h-24 w-full rounded-xl" />
        </div>
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
  const tasteEmpty =
    !taste ||
    (taste.Undoes ?? 0) + (taste.Approves ?? 0) + (taste.Denies ?? 0) + (taste.MissedReports ?? 0) ===
      0;

  return (
    <div className="mx-auto w-full max-w-7xl 2xl:max-w-[1600px]">
      <div className="mx-auto w-full max-w-4xl pb-10">
        <div className="mb-4 flex flex-wrap items-baseline gap-3">
          <h1 className="font-note text-[22px] font-semibold tracking-[0.01em]">
            感知
          </h1>
          <span className="text-[13px] text-muted-foreground">
            五感 · sensors.json · 改这里即刻生效
          </span>
          <Button
            variant="outline"
            size="sm"
            className="ml-auto self-center"
            onClick={invalidate}
          >
            <RefreshCw className="size-3" />
            刷新
          </Button>
        </div>

        {/* ── 眼 · 观察 ── */}
        <SenseCard
          dot="bg-lake"
          label="眼 · 观察"
          note="盯住目录、仓库、网页——变化按显著性分级沉淀或叫醒"
        >
          <div className="flex flex-wrap items-center gap-2 pb-1">
            <Select value={newType} onValueChange={(v) => setNewType(v)}>
              <SelectTrigger className="w-[118px]">
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
              接入
            </Button>
          </div>
          <div className={cn(HINT, "pb-1")}>
            新感官有 3 天观察期（只沉淀不叫醒）；「叫醒关键词」命中的变化会叫醒心智。
          </div>
          {eyes.length === 0 ? (
            <Empty className="border-0">
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
            <div className="divide-y divide-line border-t border-line">
              {eyes.map((sensor) => (
                <SensorRow
                  key={sensor.id}
                  sensor={sensor}
                  onToggle={commonToggle}
                  onRemove={commonRemove}
                  pending={toggle.isPending || remove.isPending}
                />
              ))}
            </div>
          )}
        </SenseCard>

        {/* ── 耳 · 听闻 ── */}
        <SenseCard
          dot="bg-plum"
          label="耳 · 听闻"
          note="外部系统说话，它在线听——webhook 推送即事件"
        >
          {ears.length === 0 ? (
            <div className={HINT}>
              未配置。用上方表单选「webhook（耳）」接入——生成 HMAC
              密钥后，外部系统 POST 到
              <code className={CODE}>
                /hook/{identityId}/&lt;感官id&gt;
              </code>
              即成为它的耳朵。
            </div>
          ) : (
            <div className="divide-y divide-line border-t border-line">
              {ears.map((sensor) => (
                <SensorRow
                  key={sensor.id}
                  sensor={sensor}
                  onToggle={commonToggle}
                  onRemove={commonRemove}
                  pending={toggle.isPending || remove.isPending}
                />
              ))}
            </div>
          )}
        </SenseCard>

        {/* ── 鼻 · 嗅探 ── */}
        <SenseCard
          dot="bg-moss"
          label="鼻 · 嗅探"
          note="自动机制，无表单"
        >
          <div className="flex flex-wrap items-center gap-1.5">
            {["速率突增", "首次出现", "阈值越界", "缺席"].map((chip) => (
              <span
                key={chip}
                className="rounded-full border border-line-strong px-2.5 py-[2.5px] font-mono text-[10.5px] text-muted-foreground"
              >
                {chip}
              </span>
            ))}
            {selfs.length > 0 ? (
              <span className="inline-flex items-center gap-1.5 rounded-full border border-primary/40 bg-primary/[0.09] px-2.5 py-[2.5px] font-mono text-[10.5px] text-foreground">
                <span className="size-[5px] rounded-full bg-primary" />
                内感受已开启 · 预算水位 / 模型熔断直达通知
              </span>
            ) : (
              <span className="rounded-full border border-dashed border-line-strong px-2.5 py-[2.5px] font-mono text-[10.5px] text-muted-foreground">
                内感受未开启
              </span>
            )}
          </div>
          <div className={cn(HINT, "mt-2")}>
            常驻自动运行：事件流的模式偏移会以
            <code className={CODE}>anomaly</code>
            事件留痕并按显著性叫醒；配置了
            <code className={CODE}>expect_every</code>
            的感官「该来的没来」会被报告。
            {selfs.length > 0 && (
              <span className="text-foreground">
                {" "}
                内感受：{selfs.map((s) => s.id).join("、")}。
              </span>
            )}
          </div>
        </SenseCard>

        {/* ── 舌 · 品评 ── */}
        <SenseCard
          dot="bg-resin"
          label="舌 · 品评"
          note="你的归因反馈是它校准的证据面（近 7 天）"
        >
          {taste ? (
            <>
              <Readout>
                <Ro
                  label="撤销"
                  value={String(taste.Undoes ?? 0)}
                  unit={
                    (taste.UndoThresholdTight ?? 0) > 0
                      ? `提案多余 ${taste.UndoThresholdTight}`
                      : undefined
                  }
                />
                <Ro label="批准" value={String(taste.Approves ?? 0)} />
                <Ro label="拒绝" value={String(taste.Denies ?? 0)} />
                <Ro
                  label="漏报匹配"
                  value={String(taste.MissedReports ?? 0)}
                  className={
                    (taste.MissedReports ?? 0) > 0 ? "text-clay" : undefined
                  }
                />
              </Readout>
              <div className={cn(HINT, "mt-2.5")}>
                {(taste.MissedReports ?? 0) > 0 && (
                  <span className="text-clay">
                    ⚠ 漏报匹配 {taste.MissedReports} 次（
                    {taste.MissedSubjects?.join("、")}）；{" "}
                  </span>
                )}
                {tasteEmpty
                  ? "窗口内还没有味觉信号——撤销时用"
                  : "撤销时用"}
                <code className={CODE}>undo --because 提案多余</code>
                类归因、审批决议都会成为它的校准证据。
              </div>
            </>
          ) : (
            <Skeleton className="h-16 w-full rounded-xl" />
          )}
        </SenseCard>

        {/* ── 身 · 触达 ── */}
        <SenseCard
          dot="bg-clay"
          label="身 · 触达"
          note="动作闭环 · 动作必须自带回读验证"
        >
          {view.robotd?.configured ? (
            <div className="space-y-1.5 text-[12px] leading-relaxed">
              <div className="flex flex-wrap items-center gap-1.5">
                <span className="text-muted-foreground">
                  已接入（mcp.json）· 模式：
                </span>
                <span
                  className={cn(
                    view.robotd.mode === "action" ? PILL_ON : PILL_WARN
                  )}
                >
                  {view.robotd.mode === "action"
                    ? "动作已授权"
                    : "观察模式（只许看不许动）"}
                </span>
              </div>
              {view.robotd.mode === "action" && (
                <div className="flex flex-wrap items-center gap-1 text-muted-foreground">
                  窗口白名单：
                  {view.robotd.allow?.map((w) => (
                    <span
                      key={w}
                      className="rounded-md bg-secondary px-2 py-[2.5px] font-mono text-[11px]"
                    >
                      {w}
                    </span>
                  ))}
                  ——动作只在这些标题的窗口上进行；密码/凭据画面在任何模式下都拒绝。
                </div>
              )}
              <div className="text-muted-foreground">
                心智用法：截屏 <code className={CODE}>look</code>
                （下一轮思考即所见）；动作
                <code className={CODE}>mcp call robotd screen_click / screen_type</code>
                （每次动作强制回读截图作为证据）。
              </div>
            </div>
          ) : (
            <div className="space-y-1.5 text-[12px] leading-relaxed text-muted-foreground">
              <div>
                未接入。接入命令（在项目目录执行，白名单按需给出；不给 =
                只许看不许动）：
              </div>
              <div className="break-all rounded-md bg-muted px-2 py-1 font-mono text-[11px]">
                mindloop mcp add robotd --identity {identityId} -- &lt;mindloop.exe 绝对路径&gt; robotd --window-allow 记事本
              </div>
              <div>
                安全链：窗口白名单谓词（动作级 tripwire）→ 敏感窗口默认拒绝 →
                每个动作强制回读验证，失败即报错 → 全程 stderr 审计留痕。
              </div>
            </div>
          )}
        </SenseCard>
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
