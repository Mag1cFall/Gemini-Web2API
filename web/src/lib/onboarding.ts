// copiedKey 是本地存储中记录已复制接入地址、API key 或客户端配置的键
const copiedKey = 'gemini-web2api.copied-endpoint'

// readEndpointCopied 读取本浏览器是否在概览或接入页复制过接入地址、API key 或客户端配置
export function readEndpointCopied() {
  try {
    return localStorage.getItem(copiedKey) === '1'
  } catch {
    return false
  }
}

// markEndpointCopied 记录已复制接入地址、API key 或客户端配置，概览的使用流程据此显示第二步已完成
export function markEndpointCopied() {
  try {
    localStorage.setItem(copiedKey, '1')
  } catch {
    // 本地存储不可用时只在本次页面内记住
  }
}
