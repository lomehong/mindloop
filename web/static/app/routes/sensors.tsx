import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Eye, Plus, RefreshCw, Trash2 } from "lucide-react";
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

/** 感知页：sensors.json 的管理面——列表/启停/新增/移除，写盘即生效
 * （心智在跑则数秒内热加载）。学习期条目带"观察期"徽标。 */
export default function SensorsPage() {
  const { identityId = "" } = useParams();
  const queryClient = useQueryClient();
  const [sensorToRemove, setSensorToRemove] = useState<SensorView | null>(
    null
  );
  // 新增表单：类型 / 路径或 URL / 议程关键词（沉淀级）/ 叫醒关键词（s2 规则）。
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

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ["sensors", identityId] });

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
      if (newType === "web") body.url = newTarget;
      else body.path = newTarget;
      const keywords = newKeywords
        .split(",")
        .map((k) => k.trim())
        .filter(Boolean);
      if (keywords.length) body.keywords = keywords;
      // 叫醒关键词 → s2 规则：命中的变化叫醒心智（其余安静沉淀）。
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
    onSuccess: (result) => {
      toast.success(
        `已接入 ${result.added}——学习期 3 天内只沉淀不叫醒`
      );
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
            感知（sensors.json）
          </h2>
          <span className="text-[11px] text-muted-foreground">
            让心智"眼里有事"：观察到的变化按显著性分级——多数安静沉淀，
            命中规则的才会叫醒。改这里即刻生效，不需要重启。
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

        {/* 新增表单 */}
        <div className="rounded-lg border p-3">
          <div className="flex flex-wrap items-center gap-2">
            <Eye className="size-4 text-muted-foreground" />
            <Select
              value={newType}
              onValueChange={(v) => setNewType(v)}
            >
              <SelectTrigger className="w-[92px]">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="file">文件目录</SelectItem>
                <SelectItem value="git">git 仓库</SelectItem>
                <SelectItem value="web">网页/RSS</SelectItem>
              </SelectContent>
            </Select>
            <Input
              className="w-72"
              placeholder={
                newType === "web" ? "https://example.com" : "要观察的目录"
              }
              value={newTarget}
              onChange={(e) => setNewTarget(e.target.value)}
            />
            <Input
              className="w-52"
              placeholder="关键词（逗号分隔，可空）"
              value={newKeywords}
              onChange={(e) => setNewKeywords(e.target.value)}
            />
            <Input
              className="w-56"
              placeholder="叫醒关键词（命中即叫醒，可空）"
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
            新感官有 3 天观察期（只沉淀不叫醒，防打扰），到期后：命中
            "叫醒关键词"的变化会叫醒心智，其余安静沉淀进记忆。
          </div>
        </div>

        {(view.sensors ?? []).length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <Eye className="size-5" />
              </EmptyMedia>
              <EmptyTitle className="text-base">还没有感官</EmptyTitle>
              <EmptyDescription>
                用上面的表单接入第一个观察目标——比如这个项目的文档目录，
                或一个你每天看的页面。
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="space-y-3">
            {(view.sensors ?? []).map((sensor) => {
              const enabled = sensor.enabled !== false;
              const target = sensor.path || sensor.url || "";
              const rules = sensor.salience?.rules ?? [];
              return (
                <div
                  key={sensor.id}
                  className={cn(
                    "rounded-lg border p-3",
                    !enabled && "opacity-60"
                  )}
                >
                  <div className="flex flex-wrap items-center gap-2">
                    <Checkbox
                      checked={enabled}
                      disabled={toggle.isPending}
                      aria-label={`${enabled ? "停用" : "启用"} ${sensor.id}`}
                      onCheckedChange={(checked) =>
                        toggle.mutate({
                          id: sensor.id,
                          enabled: checked === true,
                        })
                      }
                    />
                    <span className="font-mono text-sm font-medium">
                      {sensor.id}
                    </span>
                    <Badge variant={typeVariant(sensor.type)}>
                      {sensor.type}
                    </Badge>
                    {!enabled && <Badge variant="outline">已停用</Badge>}
                    {(sensor.learning_days ?? 0) < 0 ? null : (
                      <Badge variant="outline">观察期</Badge>
                    )}
                    <span className="ml-auto" />
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={remove.isPending}
                      onClick={() => setSensorToRemove(sensor)}
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
                          (r) =>
                            `${r.name}→${r.salience}${
                              r.min ? `(≥${r.min})` : ""
                            }`
                        )
                        .join("；")}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
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
