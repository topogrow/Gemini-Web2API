(() => {
  const invoke = (message) => window._wails.invoke(message);
  const open = (target) => invoke("desktop:open:" + new URL(target, location.href).href);
  window.open = (target) => {
    if (target) open(target);
    return null;
  };
  document.addEventListener("click", (event) => {
    const link = event.target.closest("a[href]");
    if (!link || event.defaultPrevented) return;
    if (link.target === "_blank" || link.origin !== location.origin) {
      event.preventDefault();
      open(link.href);
    }
  });

  const resizable = window._wails.environment.OS !== "darwin";
  let maximised = false;
  let pending = "";
  window.addEventListener("desktop:maximised", (event) => {
    maximised = event.detail;
  });
  const edgeAt = (event) => {
    if (!resizable || maximised) return "";
    const border = 5;
    const corner = 15;
    const { clientX: x, clientY: y } = event;
    const { innerWidth: width, innerHeight: height } = window;
    let vertical = y < border ? "n" : y >= height - border ? "s" : "";
    let horizontal = x < border ? "w" : x >= width - border ? "e" : "";
    if (vertical && !horizontal) horizontal = x < corner ? "w" : x >= width - corner ? "e" : "";
    if (horizontal && !vertical) vertical = y < corner ? "n" : y >= height - corner ? "s" : "";
    return vertical || horizontal ? vertical + horizontal + "-resize" : "";
  };
  const draggable = (target) =>
    getComputedStyle(target).getPropertyValue("--wails-draggable").trim() === "drag";
  window.addEventListener(
    "mousedown",
    (event) => {
      if (event.button !== 0) return;
      const edge = edgeAt(event);
      pending = edge ? "wails:resize:" + edge : draggable(event.target) ? "wails:drag" : "";
    },
    true,
  );
  window.addEventListener(
    "mousemove",
    (event) => {
      if (pending && event.buttons === 1) {
        invoke(pending);
        pending = "";
        return;
      }
      document.documentElement.style.cursor = edgeAt(event);
    },
    true,
  );
  window.addEventListener("mouseup", () => {
    pending = "";
  }, true);
  document.addEventListener("dblclick", (event) => {
    if (draggable(event.target)) invoke("desktop:window:toggle-maximise");
  });

  // ================= 桌面增强：服务控制栏与实时日志抽屉 =================
  const initServiceUI = () => {
    if (document.getElementById("gw-service-bar")) return;

    const bar = document.createElement("div");
    bar.id = "gw-service-bar";
    bar.style.cssText = `
      position: fixed; top: 12px; right: 90px; z-index: 99999;
      display: flex; align-items: center; gap: 8px;
      background: rgba(15, 23, 42, 0.88); backdrop-filter: blur(8px);
      padding: 5px 12px; border-radius: 20px;
      border: 1px solid rgba(255, 255, 255, 0.15);
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      font-size: 12px; color: #f8fafc; box-shadow: 0 4px 12px rgba(0,0,0,0.25);
      user-select: none;
    `;

    bar.innerHTML = `
      <span id="gw-dot" style="width:8px; height:8px; border-radius:50%; background:#4ade80; display:inline-block;"></span>
      <span id="gw-status" style="font-weight:600;">运行中 (8007)</span>
      <button id="gw-btn-toggle" style="background:#ef4444; color:#fff; border:none; border-radius:12px; padding:3px 10px; font-size:11px; cursor:pointer; font-weight:600; margin-left:4px;">停止</button>
      <button id="gw-btn-restart" style="background:#334155; color:#f8fafc; border:none; border-radius:12px; padding:3px 8px; font-size:11px; cursor:pointer;">重启</button>
      <button id="gw-btn-log" style="background:#1e293b; color:#38bdf8; border:1px solid #38bdf8; border-radius:12px; padding:3px 8px; font-size:11px; cursor:pointer;">日志</button>
    `;

    const drawer = document.createElement("div");
    drawer.id = "gw-log-drawer";
    drawer.style.cssText = `
      position: fixed; bottom: 0; left: 0; right: 0; height: 240px; z-index: 99998;
      background: rgba(10, 15, 29, 0.96); backdrop-filter: blur(12px);
      border-top: 1px solid #334155; display: none; flex-direction: column;
      font-family: Menlo, Monaco, Consolas, monospace; font-size: 11px; color: #a3e635;
      box-shadow: 0 -8px 24px rgba(0,0,0,0.5);
    `;

    drawer.innerHTML = `
      <div style="display:flex; justify-content:space-between; align-items:center; padding:6px 12px; background:#0f172a; border-bottom:1px solid #1e293b; color:#94a3b8; font-size:11px;">
        <span>Web2API 运行与自愈日志</span>
        <div style="display:flex; gap:8px;">
          <button id="gw-log-clear" style="background:transparent; color:#94a3b8; border:none; cursor:pointer;">清屏</button>
          <button id="gw-log-close" style="background:transparent; color:#f87171; border:none; cursor:pointer; font-weight:bold;">关闭</button>
        </div>
      </div>
      <div id="gw-log-body" style="flex:1; overflow-y:auto; padding:8px 12px; line-height:1.6; white-space:pre-wrap; word-break:break-all;"></div>
    `;

    document.body.appendChild(bar);
    document.body.appendChild(drawer);

    let isRunning = true;
    const dot = document.getElementById("gw-dot");
    const statusText = document.getElementById("gw-status");
    const btnToggle = document.getElementById("gw-btn-toggle");
    const btnRestart = document.getElementById("gw-btn-restart");
    const btnLog = document.getElementById("gw-btn-log");
    const logBody = document.getElementById("gw-log-body");

    const updateState = (state, port) => {
      isRunning = state === "running" || state === true;
      dot.style.background = isRunning ? "#4ade80" : "#ef4444";
      statusText.innerText = isRunning ? `运行中 (${port || 8007})` : "已停止";
      btnToggle.innerText = isRunning ? "停止" : "启动";
      btnToggle.style.background = isRunning ? "#ef4444" : "#22c55e";
    };

    btnToggle.addEventListener("click", () => {
      invoke(isRunning ? "desktop:service:stop" : "desktop:service:start");
    });
    btnRestart.addEventListener("click", () => {
      invoke("desktop:service:restart");
    });
    btnLog.addEventListener("click", () => {
      drawer.style.display = drawer.style.display === "none" ? "flex" : "none";
      if (drawer.style.display === "flex") {
        invoke("desktop:service:logs");
      }
    });
    document.getElementById("gw-log-close").addEventListener("click", () => {
      drawer.style.display = "none";
    });
    document.getElementById("gw-log-clear").addEventListener("click", () => {
      logBody.innerHTML = "";
    });

    const appendLog = (line) => {
      const row = document.createElement("div");
      row.textContent = line;
      logBody.appendChild(row);
      logBody.scrollTop = logBody.scrollHeight;
    };

    window.addEventListener("desktop:service_status", (e) => {
      const data = e.detail || {};
      updateState(data.running, data.port);
    });

    window.addEventListener("desktop:service_log", (e) => {
      if (e.detail) appendLog(e.detail);
    });

    window.addEventListener("desktop:service_logs", (e) => {
      if (Array.isArray(e.detail)) {
        logBody.innerHTML = "";
        e.detail.forEach(appendLog);
      }
    });

    // 初始状态查询
    setTimeout(() => {
      invoke("desktop:service:status");
    }, 500);
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", initServiceUI);
  } else {
    initServiceUI();
  }
})();
