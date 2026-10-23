// 首帧前按保存的选择写入配色模式，没有保存时跟随系统
document.documentElement.dataset.theme = (() => {
  try {
    const saved = localStorage.getItem('user_theme')
    if (saved === 'light' || saved === 'dark') return saved
  } catch {
    // 无法读取保存的选择时跟随系统
  }
  return matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
})()
