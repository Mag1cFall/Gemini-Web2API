// timeFormat 是时间线与实时日志行的 24 小时制时分秒
export const timeFormat = new Intl.DateTimeFormat('en-GB', {
  hour: '2-digit',
  minute: '2-digit',
  second: '2-digit',
  hour12: false,
})
