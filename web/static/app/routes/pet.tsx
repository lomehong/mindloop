import { PetApp } from "~/components/pet/pet-app";

// 桌面宠物：极简光点生命体。浏览器直接打开时是普通页面；在 Tauri 壳里
// 由托盘「宠物」装载进透明置顶窗体（desktop/src-tauri/src/pet.rs）。
export function meta() {
  return [{ title: "mindloop · 桌面宠物" }];
}

export default function Pet() {
  return <PetApp />;
}
