package remote

// indexHTML 手机端页面（v1.28 指挥台 2.0）。
// 仍然刻意做成单文件、无外部资源：现场可能只有 2G/3G 信号，
// 少一个请求就少一次"白屏等半天"。
//
// 2.0 相比 1.x 多了四件事：
//  1. 会话列表与增量输出查看（不再只能看事件流）
//  2. 电脑端的确认框可以在这里点
//  3. 断线自动重连，并用 localStorage 记住进度，回来不重复不丢
//  4. 扫二维码直接进来自动配对（配对码通过 URL hash 带过来）
const indexHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Win7 Harness 指挥台</title>
<style>
*{box-sizing:border-box}
body{margin:0;font:15px/1.6 "Microsoft YaHei",sans-serif;background:#1b1d21;color:#e6e8eb;padding-bottom:76px}
header{padding:10px 14px;background:#202329;border-bottom:1px solid #313641;position:sticky;top:0;z-index:5}
.h1{font-weight:bold}
.small{font-weight:normal;color:#a0a6b0;font-size:12px}
.tabs{display:flex;gap:6px;padding:8px 14px;background:#202329;border-bottom:1px solid #313641}
.tab{flex:1;text-align:center;padding:8px;border-radius:8px;background:#262a31;color:#a0a6b0;font-size:14px}
.tab.on{background:#33415c;color:#fff}
#log{padding:10px 14px}
.msg{margin:7px 0;padding:8px 10px;border-radius:8px;background:#23262c;white-space:pre-wrap;word-break:break-word}
.me{background:#27385a}
.sys{background:#2b2a20;color:#d9c98a}
.err{background:#3a2328;color:#ffb4be}
.tool{background:#1f2b26;color:#9fd8b6;font-size:13px}
#pair{padding:14px}
#pair input{width:100%;padding:14px;font-size:22px;letter-spacing:6px;text-align:center;background:#23262c;border:1px solid #313641;color:#e6e8eb;border-radius:8px}
#bar{position:fixed;bottom:0;left:0;right:0;display:flex;gap:8px;padding:10px 14px;background:#202329;border-top:1px solid #313641}
#bar input{flex:1;padding:12px;background:#23262c;border:1px solid #313641;color:#e6e8eb;border-radius:8px;font-size:15px}
button{padding:12px 18px;background:#4c8dff;color:#fff;border:0;border-radius:8px;font-size:15px}
button.ghost{background:#3a3f48}
button.no{background:#8d4c57}
button:disabled{opacity:.5}
.hide{display:none!important}
#ask{position:fixed;left:0;right:0;top:0;z-index:9;background:#3a3320;border-bottom:2px solid #d9c98a;padding:12px 14px}
#ask .t{font-weight:bold;color:#f0dfa6;margin-bottom:4px}
#ask .d{white-space:pre-wrap;font-size:13px;color:#ded3ae;max-height:30vh;overflow:auto;margin-bottom:8px}
#ask .row{display:flex;gap:8px}
.conv{padding:10px 14px;border-bottom:1px solid #2a2e35}
.conv .t{font-size:14px}
.conv .p{font-size:12px;color:#8f96a1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.dot{display:inline-block;width:8px;height:8px;border-radius:50%;background:#49c07a;margin-right:6px}
.dot.off{background:#c05a49}
#lines{padding:10px 14px}
#plans{padding:10px 14px}
.card{background:#23262c;border-radius:8px;padding:10px;margin:8px 0}
.card .t{font-size:14px;font-weight:bold}
.card .p{font-size:12px;color:#8f96a1;white-space:pre-wrap;word-break:break-all}
.card .r{display:flex;gap:8px;margin-top:8px}
.card .r button{padding:8px 12px;font-size:13px}
.sec{font-size:13px;color:#a0a6b0;margin:14px 0 4px}
#stopTip{font-size:12px;color:#d9c98a;position:fixed;bottom:78px;left:14px;right:14px;text-align:center}
</style>
</head>
<body>
<header>
  <div class="h1">Win7 Harness 指挥台</div>
  <div class="small"><span id="dot" class="dot off"></span><span id="st">未连接</span></div>
</header>

<div id="ask" class="hide">
  <div class="t" id="askTitle">需要确认</div>
  <div class="d" id="askDetail"></div>
  <div class="row">
    <button id="btnAllow">允许</button>
    <button id="btnDeny" class="no">拒绝</button>
  </div>
</div>

<div id="pair">
  <p class="small">在电脑上执行 /remote qr 出二维码，扫码即配对；也可手输 6 位配对码</p>
  <input id="code" inputmode="numeric" maxlength="6" placeholder="000000">
  <p><button id="btnPair">配对</button> <span class="small" id="pairTip"></span></p>
</div>

<div id="main" class="hide">
  <div class="tabs">
    <div class="tab on" id="tabLive">实时</div>
    <div class="tab" id="tabConvs">会话</div>
    <div class="tab" id="tabPlans">计划</div>
  </div>
  <div id="log"></div>
  <div id="convs" class="hide"></div>
  <div id="lines" class="hide"></div>
  <div id="plans" class="hide"></div>
</div>

<div id="bar" class="hide">
  <input id="txt" placeholder="让它干点什么…">
  <button id="btnSend">发送</button>
  <button id="btnStop" class="ghost">停止</button>
</div>
<div id="stopTip"></div>

<script>
var tok = localStorage.getItem("harness_token") || "";
var last = parseInt(localStorage.getItem("harness_seq") || "0", 10) || 0;
var liveAt = true, curConv = "", convTotal = 0, askId = "";
function $(id){return document.getElementById(id)}
function setDot(on){$("dot").className = "dot" + (on?"":" off")}
function add(text, cls){
  var d = document.createElement("div");
  d.className = "msg" + (cls?" "+cls:"");
  d.textContent = text;
  $("log").appendChild(d);
  if(liveAt) window.scrollTo(0, document.body.scrollHeight);
  while($("log").childNodes.length > 300) $("log").removeChild($("log").firstChild);
}
function api(path, body, method){
  return fetch(path, {
    method: method || "POST",
    headers: {"Content-Type":"application/json","Authorization":"Bearer "+tok},
    body: body===null?null:JSON.stringify(body||{})
  }).then(function(r){
    return r.json().catch(function(){return {}}).then(function(j){ j.__status = r.status; return j; });
  });
}
function get(path){
  return fetch(path, {headers:{"Authorization":"Bearer "+tok}})
    .then(function(r){ return r.json().catch(function(){return {}}).then(function(j){ j.__status=r.status; return j; }); });
}
function showMain(){
  $("pair").className = "hide";
  $("main").className = "";
  $("bar").className = "";
}
function needPair(){
  localStorage.removeItem("harness_token");
  tok = "";
  $("pair").className = "";
  $("main").className = "hide";
  $("bar").className = "hide";
  setDot(false);
  $("st").textContent = "需要配对";
}

/* ---------- 配对 ---------- */
function doPair(code){
  if(!code){$("pairTip").textContent = "请填配对码"; return}
  $("pairTip").textContent = "配对中…";
  api("/api/pair", {code: code}).then(function(j){
    if(j.token){
      tok = j.token;
      localStorage.setItem("harness_token", tok);
      $("pairTip").textContent = "";
      location.hash = "";
      showMain();
      startLoops();
    } else {
      $("pairTip").textContent = j.error || "配对失败";
    }
  }).catch(function(){ $("pairTip").textContent = "连不上电脑"; });
}
$("btnPair").onclick = function(){ doPair($("code").value.trim()) };

/* ---------- 实时事件流 ---------- */
function pollLive(){
  if(!tok) return;
  fetch("/api/poll?after="+last+"&wait=20", {headers:{"Authorization":"Bearer "+tok}})
    .then(function(r){
      if(r.status === 401){ needPair(); throw new Error("401") }
      return r.json();
    })
    .then(function(j){
      setDot(true);
      (j.events||[]).forEach(function(e){
        last = Math.max(last, e.seq);
        localStorage.setItem("harness_seq", last);
        var cls = e.kind==="notice" ? "sys" : (e.kind==="ask" ? "sys" : "");
        if(e.kind !== "tool") add(e.text, cls);
      });
      if(j.status){
        var s = j.status;
        $("st").textContent = (s.busy?"运行中":"空闲") + " · " + (s.model||"-") + " · 待办 " + s.pending + (s.note?(" · "+s.note):"");
      }
      pollLive();
    })
    .catch(function(){ setDot(false); setTimeout(pollLive, 3000) });
}

/* ---------- 审批 ---------- */
function pollAsk(){
  if(!tok) return;
  get("/api/approvals?wait=20").then(function(j){
    if(j.__status === 401){ return }
    var list = j.approvals || [];
    if(list.length > 0){
      var a = list[0];
      askId = a.id;
      $("askTitle").textContent = "需要确认：" + a.title;
      $("askDetail").textContent = a.detail || "";
      $("ask").className = "";
    } else if(askId){
      askId = "";
      $("ask").className = "hide";
    }
    pollAsk();
  }).catch(function(){ setTimeout(pollAsk, 4000) });
}
function answer(allow){
  if(!askId) return;
  var id = askId; askId = "";
  $("ask").className = "hide";
  api("/api/approve", {id: id, allow: allow}).then(function(j){
    if(j.error) add(j.error, "err");
  });
}
$("btnAllow").onclick = function(){ answer(true) };
$("btnDeny").onclick = function(){ answer(false) };

/* ---------- 会话列表与输出 ---------- */
function setTab(name){
  ["Live","Convs","Plans"].forEach(function(k){
    $("tab"+k).className = "tab" + (k===name ? " on" : "");
  });
  $("log").className = (name==="Live") ? "" : "hide";
  $("convs").className = "hide";
  $("lines").className = "hide";
  $("plans").className = "hide";
}
$("tabLive").onclick = function(){ liveAt = true; setTab("Live") };
$("tabConvs").onclick = function(){ liveAt = false; setTab("Convs"); loadConvs() };
$("tabPlans").onclick = function(){ liveAt = false; setTab("Plans"); loadPlans() };

/* ---------- 停止：手机上直接掐掉正在跑的任务 ---------- */
$("btnStop").onclick = function(){
  $("stopTip").textContent = "正在停止…";
  api("/api/stop", {}).then(function(j){
    if(j.__status === 401){ needPair(); return }
    $("stopTip").textContent = j.note || (j.stopped ? "已停止" : "当前没有正在跑的任务");
    setTimeout(function(){ $("stopTip").textContent = "" }, 4000);
  }).catch(function(){ $("stopTip").textContent = "连不上电脑" });
};

/* ---------- 计划 tab：定时任务开关 + 设备管理 ---------- */
function loadPlans(){
  $("plans").className = "";
  $("plans").innerHTML = '<div class="sec">定时计划</div><div class="card"><div class="p">加载中…</div></div>';
  get("/api/plans").then(function(j){
    var html = '<div class="sec">定时计划</div>';
    var list = j.plans || [];
    if(!list.length){
      html += '<div class="card"><div class="p">还没有定时计划。在电脑上执行 /sched add 登记一条。</div></div>';
    }
    $("plans").innerHTML = html;
    list.forEach(function(pl){
      var d = document.createElement("div");
      d.className = "card";
      var t = document.createElement("div");
      t.className = "t"; t.textContent = (pl.on?"● ":"○ ") + pl.name;
      var p = document.createElement("div");
      p.className = "p";
      p.textContent = pl.spec + (pl.next?("  · 下次 " + pl.next):"") + (pl.last?("  · 上次 " + pl.last):"") + "  · 已跑 " + pl.runs + " 次";
      var r = document.createElement("div");
      r.className = "r";
      var b = document.createElement("button");
      b.className = pl.on ? "ghost" : "";
      b.textContent = pl.on ? "暂停" : "启用";
      b.onclick = function(){ switchPlan(pl.id, !pl.on) };
      r.appendChild(b);
      d.appendChild(t); d.appendChild(p); d.appendChild(r);
      $("plans").appendChild(d);
    });
    loadDevices();
  }).catch(function(){
    $("plans").innerHTML = '<div class="sec">定时计划</div><div class="card"><div class="p">加载失败</div></div>';
  });
}
function switchPlan(id, on){
  api("/api/plan", {id: id, on: on}).then(function(j){
    if(j.note) add(j.note, j.ok ? "sys" : "err");
    loadPlans();
  });
}
function loadDevices(){
  get("/api/devices").then(function(j){
    var sec = document.createElement("div");
    sec.className = "sec"; sec.textContent = "已配对设备";
    $("plans").appendChild(sec);
    var list = j.devices || [];
    if(!list.length){
      var d0 = document.createElement("div");
      d0.className = "card";
      d0.innerHTML = '<div class="p">没有登记的设备（本次配对会自动登记）</div>';
      $("plans").appendChild(d0);
      return
    }
    list.forEach(function(dv){
      var d = document.createElement("div");
      d.className = "card";
      var t = document.createElement("div");
      t.className = "t"; t.textContent = dv.name;
      var p = document.createElement("div");
      p.className = "p";
      p.textContent = "最近使用 " + dv.seen + "  · 到期 " + dv.expires;
      var r = document.createElement("div");
      r.className = "r";
      var b = document.createElement("button");
      b.className = "no"; b.textContent = "下线";
      b.onclick = function(){
        if(!confirm("让 " + dv.name + " 下线？它需要重新扫码才能用。")) return;
        api("/api/revoke", {key: dv.id}).then(function(x){
          if(x.note) add(x.note, x.ok ? "sys" : "err");
          loadPlans();
        });
      };
      r.appendChild(b);
      d.appendChild(t); d.appendChild(p); d.appendChild(r);
      $("plans").appendChild(d);
    });
  });
}
function loadConvs(){
  $("convs").className = ""; $("lines").className = "hide";
  $("convs").innerHTML = '<div class="conv"><div class="p">加载中…</div></div>';
  get("/api/convs").then(function(j){
    $("convs").innerHTML = "";
    var list = j.convs || [];
    if(!list.length){ $("convs").innerHTML = '<div class="conv"><div class="p">还没有会话</div></div>'; return }
    list.forEach(function(c){
      var d = document.createElement("div");
      d.className = "conv";
      var t = document.createElement("div");
      t.className = "t"; t.textContent = c.title + "（" + c.msgs + "）";
      var p = document.createElement("div");
      p.className = "p"; p.textContent = (c.preview || "") + "  " + (c.at || "").replace("T"," ").slice(0,16);
      d.appendChild(t); d.appendChild(p);
      d.onclick = function(){ openConv(c.id) };
      $("convs").appendChild(d);
    });
  }).catch(function(){ $("convs").innerHTML = '<div class="conv"><div class="p">加载失败</div></div>' });
}
function openConv(id){
  curConv = id; convTotal = 0;
  $("convs").className = "hide"; $("lines").className = "";
  $("lines").innerHTML = '<div class="small" style="padding:4px 0"><a href="#" id="backConvs" style="color:#8ab4ff">← 返回会话列表</a></div>';
  $("backConvs").onclick = function(e){ e.preventDefault(); loadConvs(); };
  fetchConv();
}
function fetchConv(){
  if(!curConv) return;
  get("/api/conv?id="+encodeURIComponent(curConv)+"&from="+convTotal).then(function(j){
    if(j.__status !== 200){ return }
    var lines = j.lines || [];
    if(j.total < convTotal){ // 会话被截断/重置：清空重来
      curConv = curConv; convTotal = 0;
      $("lines").innerHTML = '<div class="small" style="padding:4px 0"><a href="#" id="backConvs2" style="color:#8ab4ff">← 返回会话列表</a></div>';
      $("backConvs2").onclick = function(e){ e.preventDefault(); loadConvs(); };
      return;
    }
    convTotal = j.total;
    lines.forEach(function(l){
      var cls = l.role === "user" ? "me" : (l.err ? "err" : (l.tool ? "tool" : ""));
      add(l.tool ? ("[工具] " + l.tool) : l.text, cls);
    });
  });
}
function startLoops(){
  pollLive();
  pollAsk();
}
setInterval(function(){ if(curConv) fetchConv() }, 2500);
setInterval(function(){ if(!curConv && $("convs").className === "") loadConvs() }, 10000);

/* 扫码进来时 hash 里带配对码：自动配对，省掉手输 */
(function(){
  var m = /#p=(\d{6})/.exec(location.hash || "");
  if(m && !tok){ $("code").value = m[1]; doPair(m[1]); return }
  if(tok){ showMain(); startLoops(); }
})();
</script>
</body>
</html>
`
