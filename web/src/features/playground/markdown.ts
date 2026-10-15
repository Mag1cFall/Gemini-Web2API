import MarkdownIt from 'markdown-it'

// markdown 渲染回复正文，关闭原始 HTML，链接在新标签页打开
const markdown = new MarkdownIt({ html: false, linkify: true, breaks: true })

// renderLink 是 markdown-it 原有的链接开始标签渲染
const renderLink =
  markdown.renderer.rules.link_open ??
  ((tokens, index, options, _env, self) => self.renderToken(tokens, index, options))

markdown.renderer.rules.link_open = (tokens, index, options, env, self) => {
  tokens[index]?.attrSet('target', '_blank')
  tokens[index]?.attrSet('rel', 'noreferrer')
  return renderLink(tokens, index, options, env, self)
}

// renderMarkdown 把回复正文渲染为 HTML
export function renderMarkdown(text: string) {
  return markdown.render(text)
}
