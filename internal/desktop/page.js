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
  window.addEventListener("dragover", (event) => {
    if (event.defaultPrevented) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "none";
  });
  window.addEventListener("drop", (event) => event.preventDefault());
  const locale = () => invoke("desktop:locale:" + document.documentElement.lang);
  new MutationObserver(locale).observe(document.documentElement, { attributeFilter: ["lang"] });
  locale();

  const os = window._wails.environment.OS;
  const property = (element, name) => getComputedStyle(element).getPropertyValue(name).trim();
  const draggable = (target) => property(target, "--wails-draggable") === "drag";
  if (os === "windows") {
    // Windows 由宿主按页面报告的非客户区做命中测试：标题栏中控件之外的部分为 caption，三个窗口按钮为 minimize、maximize、close
    let reported = "";
    let scheduled = false;
    const report = () => {
      scheduled = false;
      const scale = window.devicePixelRatio || 1;
      const regions = [];
      const add = (kind, left, top, right, bottom) => {
        if (right <= left || bottom <= top) return;
        regions.push({
          kind,
          left: Math.floor(left * scale),
          top: Math.floor(top * scale),
          right: Math.ceil(right * scale),
          bottom: Math.ceil(bottom * scale),
        });
      };
      for (const bar of document.querySelectorAll("header")) {
        if (!draggable(bar)) continue;
        const box = bar.getBoundingClientRect();
        const controls = [...bar.querySelectorAll("*")]
          .filter((element) => property(element, "--wails-draggable") === "no-drag")
          .map((element) => element.getBoundingClientRect())
          .filter((rect) => rect.width > 0)
          .sort((a, b) => a.left - b.left);
        let left = box.left;
        for (const rect of controls) {
          add("caption", left, box.top, rect.left, box.bottom);
          left = Math.max(left, rect.right);
        }
        add("caption", left, box.top, box.right, box.bottom);
        for (const button of bar.querySelectorAll("button")) {
          const kind = property(button, "--wails-non-client-region");
          const rect = button.getBoundingClientRect();
          if (["minimize", "maximize", "close"].includes(kind)) add(kind, rect.left, rect.top, rect.right, rect.bottom);
        }
      }
      const payload = JSON.stringify({ version: 1, regions });
      if (payload === reported) return;
      reported = payload;
      invoke("wails:non-client-region:" + payload);
    };
    const schedule = () => {
      if (scheduled) return;
      scheduled = true;
      requestAnimationFrame(report);
    };
    new MutationObserver(schedule).observe(document.documentElement, { attributes: true, childList: true, subtree: true });
    window.addEventListener("resize", schedule);
    schedule();
    return;
  }

  const resizable = os === "linux";
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
})();
