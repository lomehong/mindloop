/** 对话发送者名字的共享存储（对话单真相源 §3：舞台对话条 / 全屏
 * 浮层 / 对话页 / PWA 同一 profile——同一 localStorage 键同一语义）。*/
export const MY_NAME_KEY = "shellm-chat-from";

/** 用户在对话界面明确选过的名字（持久化），空串表示从未设置。 */
export function storedName(): string {
  if (typeof window === "undefined") return "";
  return window.localStorage.getItem(MY_NAME_KEY) || "";
}

/** 用户是否碰过名字字段。清空也存 ""（算碰过），chatrc 默认值
 * 不得在重挂载时回填——语义与 chat.tsx 原实现一致。 */
export function hasStoredName(): boolean {
  if (typeof window === "undefined") return false;
  return window.localStorage.getItem(MY_NAME_KEY) !== null;
}

export function saveName(name: string): void {
  try {
    window.localStorage.setItem(MY_NAME_KEY, name);
  } catch {
    /* 存储不可用：名字退化为会话内状态 */
  }
}
