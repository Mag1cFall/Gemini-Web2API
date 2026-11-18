// 资源载入失败或入口模块解析失败时以网络正文覆盖本页资源的 HTTP 缓存后重新载入，30 秒内只重试一次
;(() => {
  const recover = (urls) => {
    if (Date.now() - Number(sessionStorage.getItem('asset-recovery')) < 30000) return
    sessionStorage.setItem('asset-recovery', String(Date.now()))
    const assets = new Set(urls)
    for (const entry of performance.getEntriesByType('resource')) assets.add(entry.name)
    for (const node of document.querySelectorAll('script[src], link[href]'))
      assets.add(node.src || node.href)
    const targets = [...assets].filter((url) =>
      new URL(url, location.href).pathname.includes('/assets/'),
    )
    void Promise.allSettled(targets.map((url) => fetch(url, { cache: 'reload' }))).then(() =>
      location.reload(),
    )
  }
  addEventListener(
    'error',
    (event) => {
      const target = event.target
      if (target instanceof HTMLScriptElement || target instanceof HTMLLinkElement)
        recover([target.src || target.href])
      else if (event.error instanceof SyntaxError && !document.querySelector('#root > *'))
        recover([event.filename])
    },
    true,
  )
  addEventListener('vite:preloadError', (event) =>
    recover(String(event.payload).match(/\/assets\/[^\s'"]+/g) ?? []),
  )
})()
