package web

// system_page.go — 统一配置面的服务端直出页（docs/designs/system.md
// §3）：能力总览 + 按身份开关 + 感官管理。单文件 HTML + 原生 fetch，
// 不依赖 SPA 构建（React 系统页后续立项）。鉴权与 SPA 同款：页面
// 上的令牌只进 Authorization 头，不落 cookie。

import "net/http"

const systemPageHTML = `<!doctype html>
<html lang="zh"><head><meta charset="utf-8"><title>mindloop 系统</title>
<style>
body{font-family:system-ui,sans-serif;margin:24px;max-width:900px;color:#222}
h2{border-bottom:2px solid #ddd;padding-bottom:4px}
.card{border:1px solid #ddd;border-radius:8px;padding:12px 16px;margin:12px 0}
.row{display:flex;align-items:center;gap:8px;margin:6px 0;flex-wrap:wrap}
button{padding:4px 12px;cursor:pointer}
input,select{padding:4px 8px}
.ok{color:#0a7d32}.err{color:#b3261e}
code{background:#f4f4f4;padding:1px 5px;border-radius:4px}
</style></head><body>
<h1>mindloop 系统</h1>
<div class="row">控制面令牌：<input id="token" type="password" size="28">
<button onclick="loadAll()">载入</button><span id="authmsg"></span></div>

<h2>能力总览（system.json）</h2>
<div id="status" class="card">（未载入）</div>
<div class="card">
  <div class="row"><label><input type="checkbox" id="web-enabled"> web 仪表盘</label>
  host <input id="web-host" size="12"> port <input id="web-port" size="5"></div>
  <div id="idents"></div>
  <div class="row"><button onclick="saveSystem()">保存（宿主 5 秒内热加载）</button><span id="sysmsg"></span></div>
</div>

<h2>感官管理</h2>
<div class="row">身份：<input id="ident-name" size="10" value="ada">
<button onclick="loadSensors()">载入感官</button></div>
<div id="sensors"></div>
<div class="card">
  <div class="row">新增：
    <select id="ns-type"><option>file</option><option>git</option><option>web</option></select>
    <input id="ns-id" size="10" placeholder="id(可空)">
    <input id="ns-target" size="34" placeholder="路径或 URL">
    <input id="ns-keywords" size="18" placeholder="关键词,逗号分隔">
    <button onclick="addSensor()">接入</button></div>
  <span id="smsg"></span>
</div>

<script>
let H = () => ({"Authorization":"Bearer "+document.getElementById("token").value,"Content-Type":"application/json"});
let j = async (u,o) => { const r = await fetch(u,o); const t = await r.text();
  if (!r.ok) throw new Error(r.status+" "+t); return t?JSON.parse(t):null; };
// esc 是插值进 innerHTML 的唯一入口：身份名/感官配置是数据不是标记
//（可经 API 与审批提案写入），不转义就是存储型 XSS→控制面令牌窃取。
let esc = s => String(s==null?"":s).replace(/[&<>"']/g, function(c){
  return {"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;","'":"&#39;"}[c]; });

async function loadAll(){
  try{
    const d = await j("/api/system",{headers:H()});
    const c = d.config;
    document.getElementById("web-enabled").checked = !!c.web.enabled;
    document.getElementById("web-host").value = c.web.host||"127.0.0.1";
    document.getElementById("web-port").value = c.web.port||8080;
    const box = document.getElementById("idents"); box.innerHTML="";
    const names = Object.keys(c.identities||{});
    if(!names.length) box.innerHTML="<em>（无身份——mindloop identity create 创建后重新载入）</em>";
    for(const n of names){
      const it = c.identities[n];
      const row = document.createElement("div"); row.className="row";
      row.innerHTML = '<b>'+esc(n)+'</b> ' +
        '<label><input type="checkbox" data-i="'+esc(n)+'" data-k="enabled" '+(it.enabled?"checked":"")+'>启用</label> ' +
        '<label><input type="checkbox" data-i="'+esc(n)+'" data-k="mind" '+(it.mind?"checked":"")+'>心智</label> ' +
        '<label><input type="checkbox" data-i="'+esc(n)+'" data-k="wecom" '+(it.wecom?"checked":"")+'>企微桥</label>';
      box.appendChild(row);
    }
    document.getElementById("status").textContent = JSON.stringify(d.status||{}, null, 1);
    document.getElementById("authmsg").innerHTML = '<span class="ok">已载入</span>';
  }catch(e){ document.getElementById("authmsg").innerHTML = '<span class="err">'+esc(e.message)+'</span>'; }
}

async function saveSystem(){
  const idents = {};
  document.querySelectorAll("#idents input[type=checkbox]").forEach(cb=>{
    const n = cb.dataset.i, k = cb.dataset.k;
    idents[n] = idents[n]||{enabled:false,mind:false,wecom:false};
    idents[n][k] = cb.checked;
  });
  const cfg = {version:1, web:{enabled:document.getElementById("web-enabled").checked,
    host:document.getElementById("web-host").value, port:+document.getElementById("web-port").value||0},
    identities:idents};
  try{ await j("/api/system",{method:"PUT",headers:H(),body:JSON.stringify(cfg)});
    document.getElementById("sysmsg").innerHTML='<span class="ok">已保存</span>';
  }catch(e){ document.getElementById("sysmsg").innerHTML='<span class="err">'+esc(e.message)+'</span>'; }
}

async function loadSensors(){
  const n = document.getElementById("ident-name").value;
  try{
    const d = await j("/api/identities/"+encodeURIComponent(n)+"/sensors",{headers:H()});
    const box = document.getElementById("sensors"); box.innerHTML="";
    if(!(d.sensors||[]).length) box.innerHTML="<em>（未配置）</em>";
    for(const s of d.sensors||[]){
      // 逐节点构建：id/target 是配置数据（可含任意字符），一律走
      // textContent——旧实现把 id 拼进内联 onclick 字符串，是存储型
      // XSS 的入口（配置可经 API/审批提案写入，页面又持有控制面令牌）。
      const row=document.createElement("div"); row.className="row";
      const codeEl=document.createElement("code"); codeEl.textContent=s.id; row.appendChild(codeEl);
      row.appendChild(document.createTextNode(" ["+(s.type||"")+"] "+(s.path||s.url||"")+" "));
      const lab=document.createElement("label");
      const cb=document.createElement("input"); cb.type="checkbox"; cb.checked=(s.enabled!==false);
      cb.addEventListener("change",function(){ toggleSensor(s.id,cb.checked); });
      lab.appendChild(cb); lab.appendChild(document.createTextNode("启用"));
      row.appendChild(lab);
      const btn=document.createElement("button"); btn.textContent="移除";
      btn.addEventListener("click",function(){ rmSensor(s.id); });
      row.appendChild(btn);
      box.appendChild(row);
    }
  }catch(e){ document.getElementById("smsg").innerHTML='<span class="err">'+esc(e.message)+'</span>'; }
}
async function toggleSensor(id,enabled){
  const n = document.getElementById("ident-name").value;
  await j("/api/identities/"+encodeURIComponent(n)+"/sensors/"+encodeURIComponent(id)+"/enabled",{method:"PUT",headers:H(),body:JSON.stringify({enabled})});
  loadSensors();
}
async function rmSensor(id){
  const n = document.getElementById("ident-name").value;
  await j("/api/identities/"+encodeURIComponent(n)+"/sensors/"+encodeURIComponent(id),{method:"DELETE",headers:H()});
  loadSensors();
}
async function addSensor(){
  const n = document.getElementById("ident-name").value;
  const body = {type:document.getElementById("ns-type").value,
    id:document.getElementById("ns-id").value};
  const t = document.getElementById("ns-target").value;
  if(document.getElementById("ns-type").value==="web") body.url=t; else body.path=t;
  const kws = document.getElementById("ns-keywords").value.split(",").map(x=>x.trim()).filter(Boolean);
  if(kws.length) body.keywords=kws;
  try{ await j("/api/identities/"+encodeURIComponent(n)+"/sensors",{method:"POST",headers:H(),body:JSON.stringify(body)});
    document.getElementById("smsg").innerHTML='<span class="ok">已接入（宿主在跑则 5 秒内生效）</span>'; loadSensors();
  }catch(e){ document.getElementById("smsg").innerHTML='<span class="err">'+esc(e.message)+'</span>'; }
}
</script></body></html>`

func (s *Server) systemPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(systemPageHTML))
}
