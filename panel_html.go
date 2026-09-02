package main

// panelHTML is the management panel. It is served from the unauthenticated
// resource route, so it reads the CPA management key from the host page's
// storage and calls the authenticated data routes itself.
const panelHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>u1s1 · 额度与用量</title>
<style>
:root{
  --bg:#0f172a; --card:#1e293b; --raise:#334155;
  --fg:#f8fafc; --dim:#94a3b8; --line:#334155;
  --accent:#3b82f6; --ok:#10b981; --warn:#f59e0b; --err:#ef4444; --r:12px;
}
@media(prefers-color-scheme:light){
  :root{--bg:#f8fafc;--card:#fff;--raise:#f1f5f9;--fg:#0f172a;--dim:#64748b;--line:#e2e8f0}
}
*{box-sizing:border-box;margin:0;padding:0}
body{font:14px/1.5 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;background:var(--bg);color:var(--fg);padding:24px}
h1{font-size:22px;font-weight:700}
h2{font-size:16px;font-weight:600;margin-bottom:12px}
a{color:var(--accent)}
.head{padding-bottom:14px;margin-bottom:14px;border-bottom:1px solid var(--line)}
.head p{color:var(--dim);font-size:13px;margin-top:4px}
.bar{display:flex;gap:8px;flex-wrap:wrap;align-items:center;margin-bottom:22px}
.btn{border:0;border-radius:8px;padding:8px 14px;background:var(--accent);color:#fff;font:inherit;font-weight:500;cursor:pointer}
.btn:hover{filter:brightness(1.08)}
.btn:disabled{opacity:.55;cursor:default}
.btn.sec{background:var(--raise);color:var(--fg)}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:14px;margin-bottom:24px}
.card{background:var(--card);border:1px solid var(--line);border-radius:var(--r);padding:18px}
.k{color:var(--dim);font-size:12px}
.v{font-size:24px;font-weight:700;margin-top:2px}
.acct{background:var(--card);border:1px solid var(--line);border-radius:var(--r);padding:18px;margin-bottom:14px}
.acct-top{display:flex;justify-content:space-between;align-items:center;gap:12px;margin-bottom:14px;flex-wrap:wrap}
.who{font-size:15px;font-weight:600}
.who small{color:var(--dim);font-weight:400;margin-left:6px}
.pill{border-radius:999px;padding:2px 10px;font-size:12px;background:rgba(16,185,129,.15);color:var(--ok)}
.pill.off{background:rgba(239,68,68,.15);color:var(--err)}
.mini{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:10px;margin-bottom:14px}
.mini div{background:var(--bg);border-radius:8px;padding:10px}
.mini .k{font-size:11px;text-transform:uppercase}
.mini .v{font-size:16px;font-weight:600}
table{width:100%;border-collapse:collapse;font-size:13px}
th,td{padding:7px 10px;text-align:left;border-bottom:1px solid var(--line)}
th{color:var(--dim);font-weight:500}
.claims{display:flex;flex-wrap:wrap;gap:8px;margin-top:12px}
.claim{border:1px solid var(--line);border-radius:8px;padding:8px 12px;font-size:12px;background:var(--bg)}
.claim b{display:block;font-size:13px;margin-bottom:2px}
.claim.on{border-color:var(--warn)}
.claim.on b{color:var(--warn)}
.note{color:var(--dim);font-size:12px;margin-top:10px}
.err{color:var(--err);font-size:12px;margin-top:8px}
.empty{text-align:center;padding:42px 16px;color:var(--dim)}
.spin{display:inline-block;width:14px;height:14px;border:2px solid var(--dim);border-top-color:transparent;border-radius:50%;animation:s .7s linear infinite;vertical-align:-2px}
@keyframes s{to{transform:rotate(360deg)}}
.auth{background:var(--card);border:1px solid var(--line);border-radius:var(--r);padding:14px;margin-bottom:18px;display:none;gap:10px;align-items:center;flex-wrap:wrap}
.auth.show{display:flex}
.auth input{flex:1;min-width:220px;background:var(--bg);border:1px solid var(--line);color:var(--fg);padding:8px 10px;border-radius:6px;font:inherit}
.modal{display:none;position:fixed;inset:0;background:rgba(0,0,0,.6);align-items:center;justify-content:center;padding:20px;z-index:9}
.modal.show{display:flex}
.modal .box{background:var(--card);border-radius:var(--r);padding:20px;width:min(560px,100%)}
.modal textarea{width:100%;height:170px;margin:10px 0;background:var(--bg);border:1px solid var(--line);color:var(--fg);border-radius:6px;padding:10px;font:12px/1.4 ui-monospace,Menlo,monospace}
.modal .act{display:flex;justify-content:flex-end;gap:8px}
</style>
</head>
<body>

<div class="head">
  <h1>u1s1 额度与用量</h1>
  <p>账号余额、用量包明细、领取状态与 CPA 代理调用统计</p>
</div>

<div class="bar">
  <button class="btn" id="refresh">刷新</button>
  <button class="btn sec" id="open-import">导入 CLI 凭证</button>
  <span id="status" class="k"></span>
</div>

<div class="auth" id="auth">
  <span>CPA 管理密钥</span>
  <input type="password" id="key" placeholder="未能自动获取，请粘贴 Management Key">
  <button class="btn sec" id="save-key">保存并加载</button>
</div>

<div class="grid">
  <div class="card"><div class="k">可用余额</div><div class="v" id="t-usd">—</div></div>
  <div class="card"><div class="k">用量包剩余 Token</div><div class="v" id="t-tok">—</div></div>
  <div class="card"><div class="k">代理成功</div><div class="v" id="t-ok" style="color:var(--ok)">—</div></div>
  <div class="card"><div class="k">代理失败</div><div class="v" id="t-err" style="color:var(--err)">—</div></div>
</div>

<h2>账号</h2>
<div id="list"><div class="empty"><span class="spin"></span> 正在加载…</div></div>

<div class="modal" id="import">
  <div class="box">
    <h2>导入 ~/.u1s1/config.json</h2>
    <p class="k">粘贴已登录设备的凭证内容，校验通过后会写入 CPA 认证目录。</p>
    <textarea id="import-json" placeholder='{"deviceToken":"u1s1d-…","devicePublicJwk":{…},"devicePrivateJwk":{…}}'></textarea>
    <div class="act">
      <button class="btn sec" id="close-import">取消</button>
      <button class="btn" id="do-import">导入</button>
    </div>
  </div>
</div>

<script>
const LOCAL_KEY = 'u1s1-cpa.management-key';
const BASE = '/v0/management/plugins/u1s1';
const el = (id) => document.getElementById(id);

// --- management key discovery ---------------------------------------------
// CPA's web UI stores its auth blob under 'cli-proxy-auth', obfuscated with a
// host+UA derived XOR key. Read it from this frame and the parent so the panel
// works both standalone and embedded.
function deobfuscate(raw) {
  const PREFIX = 'enc::v1::';
  const SALT = 'cli-proxy-api-webui::secure-storage';
  const s = String(raw ?? '');
  if (!s.startsWith(PREFIX)) return s;
  try {
    const bin = atob(s.slice(PREFIX.length));
    const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0));
    let key;
    try {
      key = new TextEncoder().encode(SALT + '|' + location.host + '|' + navigator.userAgent);
    } catch { key = new TextEncoder().encode(SALT); }
    return new TextDecoder().decode(bytes.map((b, i) => b ^ key[i % key.length]));
  } catch { return s; }
}

function stores() {
  const out = [];
  for (const get of [
    () => window.localStorage,
    () => window.sessionStorage,
    () => window.parent !== window && window.parent.localStorage,
    () => window.parent !== window && window.parent.sessionStorage,
  ]) {
    try { const s = get(); if (s) out.push(s); } catch {}
  }
  return out;
}

function managementKey() {
  try {
    const saved = localStorage.getItem(LOCAL_KEY);
    if (saved) return saved;
  } catch {}
  for (const store of stores()) {
    try {
      const raw = store.getItem('cli-proxy-auth');
      if (!raw) continue;
      const parsed = JSON.parse(deobfuscate(raw));
      const key = parsed?.state?.managementKey || parsed?.managementKey;
      if (key && String(key).trim()) return String(key).trim();
    } catch {}
  }
  return '';
}

// --- data ------------------------------------------------------------------
let busy = false;

async function call(path, init = {}) {
  const key = managementKey();
  const headers = { ...(init.headers || {}) };
  if (key) headers.Authorization = 'Bearer ' + key;
  const resp = await fetch(BASE + path, { ...init, headers });
  if (resp.status === 401 || resp.status === 403) {
    el('auth').classList.add('show');
    if (key) el('key').value = key;
    throw new Error('需要 CPA 管理密钥');
  }
  const data = await resp.json().catch(() => null);
  if (!resp.ok) throw new Error(data?.error || ('HTTP ' + resp.status));
  return data;
}

async function load(refresh = false) {
  if (busy) return;
  busy = true;
  el('refresh').disabled = true;
  el('status').innerHTML = '<span class="spin"></span> 读取账号数据…';
  if (refresh) el('list').innerHTML = '<div class="empty"><span class="spin"></span> 正在刷新…</div>';

  try {
    const data = await call(refresh ? '/refresh' : '/data', refresh ? { method: 'POST' } : {});
    render(data);
    el('status').textContent = '更新于 ' + new Date(data.updated_at).toLocaleTimeString();
  } catch (e) {
    el('status').textContent = '';
    el('list').innerHTML = '<div class="empty">加载失败：' + esc(e.message) + '</div>';
  } finally {
    busy = false;
    el('refresh').disabled = false;
  }
}

// --- render ----------------------------------------------------------------
const esc = (v) => String(v ?? '').replace(/[&<>"']/g, (c) =>
  ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
const num = (v) => Number(v || 0).toLocaleString('en-US');
const usd = (v) => '$' + Number(v || 0).toFixed(4);

function tokensCn(v) {
  const n = Number(v || 0);
  if (n >= 1e8) return (Math.round(n / 1e7) / 10).toLocaleString('en-US') + ' 亿';
  if (n >= 1e4) return Math.round(n / 1e4).toLocaleString('en-US') + ' 万';
  return num(n);
}

function render(d) {
  el('t-usd').textContent = usd(d.total_usd);
  el('t-tok').textContent = tokensCn(d.total_tokens);
  el('t-ok').textContent = num(d.total_success);
  el('t-err').textContent = num(d.total_failed);

  if (!d.accounts?.length) {
    el('list').innerHTML = '<div class="empty">未找到 u1s1 凭证。点击「导入 CLI 凭证」，' +
      '或把 ~/.u1s1/config.json 放进 CPA 认证目录并命名为 u1s1-*.json。</div>';
    return;
  }
  el('list').innerHTML = d.accounts.map((a) => accountCard(a, d.dashboard_url)).join('');
}

function accountCard(a, dashboard) {
  const acc = a.account;
  const rows = (acc?.packages || []).map((p) => {
    const limit = p.daily_tokens ?? p.total_tokens ?? 0;
    const daily = p.daily_tokens != null;
    return '<tr><td>' + esc(p.kind) + '</td>' +
      '<td>' + tokensCn(limit) + (daily ? '/天' : '') + '</td>' +
      '<td>' + tokensCn(p.used_today) + '</td>' +
      '<td><b>' + tokensCn(p.remaining) + '</b></td>' +
      '<td>' + esc(p.expires_at || '永不过期') + '</td>' +
      '<td>' + esc(p.note || '—') + '</td></tr>';
  }).join('');

  const claims = (a.claims || []).map((c) =>
    '<div class="claim' + (c.available ? ' on' : '') + '">' +
    '<b>' + esc(c.label) + (c.available ? ' · 待领取' : '') + '</b>' +
    '<span>' + esc(c.detail) + '</span>' +
    (c.available ? ' <a href="' + esc(c.url) + '" target="_blank" style="margin-left:6px;font-weight:600;">去领取 →</a>' : '') +
    '</div>').join('');

  return '<div class="acct">' +
    '<div class="acct-top">' +
      '<div class="who">' + esc(a.email || a.label || a.id) +
        '<small>' + esc(a.id) + '</small></div>' +
      '<span class="pill' + (a.disabled ? ' off' : '') + '">' +
        (a.disabled ? '已禁用' : '正常') + '</span>' +
    '</div>' +
    '<div class="mini">' +
      '<div><div class="k">可用余额</div><div class="v">' + (acc ? usd(acc.remaining_usd) : '—') + '</div></div>' +
      '<div><div class="k">本月已用</div><div class="v">' + (acc ? usd(acc.mtd_usd) : '—') + '</div></div>' +
      '<div><div class="k">今日免费剩余</div><div class="v">' + (acc ? usd(acc.daily_free_remaining_usd) : '—') + '</div></div>' +
      '<div><div class="k">代理成功 / 失败</div><div class="v">' +
        '<span style="color:var(--ok)">' + num(a.success) + '</span> / ' +
        '<span style="color:var(--err)">' + num(a.failed) + '</span></div></div>' +
    '</div>' +
    (rows ? '<table><thead><tr><th>类型</th><th>额度</th><th>今日消耗</th>' +
      '<th>剩余</th><th>到期</th><th>说明</th></tr></thead><tbody>' + rows + '</tbody></table>' : '') +
    (claims ? '<div class="claims">' + claims + '</div>' : '') +
    (a.error ? '<div class="err">' + esc(a.error) + '</div>' : '') +
  '</div>';
}

// --- events ----------------------------------------------------------------
el('refresh').onclick = () => load(true);
el('save-key').onclick = () => {
  const v = el('key').value.trim();
  if (v) { try { localStorage.setItem(LOCAL_KEY, v); } catch {} }
  el('auth').classList.remove('show');
  load();
};
el('open-import').onclick = () => el('import').classList.add('show');
el('close-import').onclick = () => el('import').classList.remove('show');
el('do-import').onclick = async () => {
  const raw = el('import-json').value.trim();
  if (!raw) return;
  const btn = el('do-import');
  btn.disabled = true;
  btn.textContent = '校验中…';
  try {
    const res = await call('/import', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: raw,
    });
    el('import').classList.remove('show');
    el('import-json').value = '';
    el('status').textContent = '已导入 ' + (res.email || res.file);
    load(true);
  } catch (e) {
    alert('导入失败：' + e.message);
  } finally {
    btn.disabled = false;
    btn.textContent = '导入';
  }
};

load();
</script>
</body>
</html>`
