// 统一的确认对话框：替代散落各页的 window.confirm。
// - 原生 confirm 阻塞主线程、不可主题化，且全部操作同等确认强度；
//   这里按危险级别分级：tone="danger" 渲染红色确认按钮，用于
//   删除类（.env 变量、技能、导出记录）与全部停止/强制停止类操作；
//   普通耗时操作（重算、重建）用默认样式。
// - 文案统一中文，标题一行问句 + 描述区补充后果说明。

import * as React from "react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "~/components/ui/alert-dialog";
import { buttonVariants } from "~/components/ui/button";
import { cn } from "~/lib/utils";

export interface ConfirmDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  /** 后果说明；支持多行文本（whitespace-pre-wrap）。 */
  description?: React.ReactNode;
  confirmText?: string;
  cancelText?: string;
  /** "danger" → 红色确认按钮：删除类、全部停止类等高危操作。 */
  tone?: "default" | "danger";
  /** 点按确认后执行；对话框的关闭由组件负责。 */
  onConfirm: () => void;
  /** 用户取消（取消按钮或 Escape）时回调；确认关闭不触发。 */
  onCancel?: () => void;
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmText = "确认",
  cancelText = "取消",
  tone = "default",
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  // Radix 在 Action 点击后也会走 onOpenChange(false)；用标记区分
  // 「确认关闭」与「取消关闭」，onCancel 只在真正的取消路径触发。
  // 这两个回调都在事件期执行（非渲染期），用 ref 记录是安全的。
  const confirmingRef = React.useRef(false);

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next && !confirmingRef.current) onCancel?.();
        confirmingRef.current = false;
        onOpenChange(next);
      }}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          {description !== undefined && description !== null && description !== "" && (
            <AlertDialogDescription className="whitespace-pre-wrap">
              {description}
            </AlertDialogDescription>
          )}
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{cancelText}</AlertDialogCancel>
          <AlertDialogAction
            className={cn(
              tone === "danger" && buttonVariants({ variant: "destructive" })
            )}
            onClick={() => {
              confirmingRef.current = true;
              onConfirm();
            }}
          >
            {confirmText}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
