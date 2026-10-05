// 桌面壳内不呈现手机（PWA）面：把 /talk* 路径映射回桌面等价路由。
// 手机面只在浏览器/PWA 场景存在（见 docs/designs/ui-language.md §7 F）。
export function desktopPathFromTalk(pathname: string): string {
  const match = /^\/talk\/([^/]+)(\/tasks)?\/?$/.exec(pathname);
  if (!match) return "/";
  const id = match[1];
  return match[2] ? `/i/${id}/tasks` : `/i/${id}/chat`;
}
