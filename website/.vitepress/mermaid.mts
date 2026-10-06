// Turns ```mermaid fences into the client-side <Mermaid> component. The
// diagram source is URI-encoded so Vue's template compiler sees plain text.
import type MarkdownIt from 'markdown-it'

export function mermaidFences(md: MarkdownIt) {
  const fence = md.renderer.rules.fence!
  md.renderer.rules.fence = (tokens, index, options, env, self) => {
    const token = tokens[index]
    if (token.info.trim() !== 'mermaid') return fence(tokens, index, options, env, self)
    return `<Mermaid code="${encodeURIComponent(token.content)}" />\n`
  }
}
