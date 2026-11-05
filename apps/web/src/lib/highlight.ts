import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import c from 'highlight.js/lib/languages/c'
import cpp from 'highlight.js/lib/languages/cpp'
import csharp from 'highlight.js/lib/languages/csharp'
import css from 'highlight.js/lib/languages/css'
import dart from 'highlight.js/lib/languages/dart'
import diff from 'highlight.js/lib/languages/diff'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import go from 'highlight.js/lib/languages/go'
import graphql from 'highlight.js/lib/languages/graphql'
import ini from 'highlight.js/lib/languages/ini'
import java from 'highlight.js/lib/languages/java'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import kotlin from 'highlight.js/lib/languages/kotlin'
import less from 'highlight.js/lib/languages/less'
import lua from 'highlight.js/lib/languages/lua'
import makefile from 'highlight.js/lib/languages/makefile'
import markdown from 'highlight.js/lib/languages/markdown'
import nginx from 'highlight.js/lib/languages/nginx'
import php from 'highlight.js/lib/languages/php'
import powershell from 'highlight.js/lib/languages/powershell'
import protobuf from 'highlight.js/lib/languages/protobuf'
import python from 'highlight.js/lib/languages/python'
import ruby from 'highlight.js/lib/languages/ruby'
import rust from 'highlight.js/lib/languages/rust'
import scala from 'highlight.js/lib/languages/scala'
import scss from 'highlight.js/lib/languages/scss'
import sql from 'highlight.js/lib/languages/sql'
import swift from 'highlight.js/lib/languages/swift'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'

for (const [name, lang] of Object.entries({
  bash, c, cpp, csharp, css, dart, diff, dockerfile, go, graphql, ini, java, javascript, json, kotlin, less, lua,
  makefile, markdown, nginx, php, powershell, protobuf, python, ruby, rust, scala, scss, sql, swift, typescript, xml, yaml,
})) {
  hljs.registerLanguage(name, lang)
}
hljs.registerAliases(['sh', 'shell', 'zsh', 'console'], { languageName: 'bash' })
hljs.registerAliases(['js', 'jsx', 'mjs'], { languageName: 'javascript' })
hljs.registerAliases(['ts', 'tsx'], { languageName: 'typescript' })
hljs.registerAliases(['html', 'svg', 'vue'], { languageName: 'xml' })
hljs.registerAliases(['yml', 'toml'], { languageName: 'yaml' })
hljs.registerAliases(['py'], { languageName: 'python' })
hljs.registerAliases(['golang'], { languageName: 'go' })
hljs.registerAliases(['md'], { languageName: 'markdown' })
hljs.registerAliases(['rs'], { languageName: 'rust' })
hljs.registerAliases(['cs'], { languageName: 'csharp' })
hljs.registerAliases(['rb'], { languageName: 'ruby' })
hljs.registerAliases(['kt'], { languageName: 'kotlin' })
hljs.registerAliases(['ps1'], { languageName: 'powershell' })
hljs.registerAliases(['proto'], { languageName: 'protobuf' })

export { hljs }

const EXT: Record<string, string> = {
  md: 'markdown', markdown: 'markdown', mdx: 'markdown',
  js: 'javascript', jsx: 'javascript', mjs: 'javascript', cjs: 'javascript',
  ts: 'typescript', tsx: 'typescript', mts: 'typescript', cts: 'typescript',
  json: 'json', jsonc: 'json', json5: 'json', webmanifest: 'json',
  html: 'xml', htm: 'xml', xml: 'xml', svg: 'xml', vue: 'xml', plist: 'xml', xhtml: 'xml',
  css: 'css', scss: 'scss', sass: 'scss', less: 'less',
  go: 'go', rs: 'rust', py: 'python', pyi: 'python', rb: 'ruby', php: 'php', lua: 'lua',
  java: 'java', kt: 'kotlin', kts: 'kotlin', scala: 'scala', swift: 'swift', dart: 'dart',
  c: 'c', h: 'c', cc: 'cpp', cpp: 'cpp', cxx: 'cpp', hpp: 'cpp', hh: 'cpp', cs: 'csharp',
  sh: 'bash', bash: 'bash', zsh: 'bash', env: 'bash', ps1: 'powershell',
  sql: 'sql', graphql: 'graphql', gql: 'graphql', proto: 'protobuf',
  yml: 'yaml', yaml: 'yaml', toml: 'ini', ini: 'ini', cfg: 'ini', conf: 'ini', properties: 'ini',
  diff: 'diff', patch: 'diff', mk: 'makefile', nginx: 'nginx',
}
const NAMES: Record<string, string> = { dockerfile: 'dockerfile', makefile: 'makefile', gnumakefile: 'makefile' }

/** The highlight.js language for a file path, '' when unknown. */
export function languageOf(path: string): string {
  const base = (path.split('/').pop() ?? '').toLowerCase()
  if (NAMES[base]) return NAMES[base]
  if (base.startsWith('.env')) return 'bash'
  const i = base.lastIndexOf('.')
  return i < 0 ? '' : EXT[base.slice(i + 1)] ?? ''
}

export function isMarkdownPath(path: string): boolean {
  return languageOf(path) === 'markdown'
}

const IMAGE: Record<string, string> = {
  png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp',
  bmp: 'image/bmp', ico: 'image/x-icon', avif: 'image/avif', svg: 'image/svg+xml',
}

/** The MIME type of an image file path, '' when it is not an image. */
export function imageType(path: string): string {
  const base = (path.split('/').pop() ?? '').toLowerCase()
  return IMAGE[base.slice(base.lastIndexOf('.') + 1)] ?? ''
}

/** Highlighted HTML (escaped) for code; unknown languages come back escaped and plain. */
export function highlightCode(code: string, lang: string): string {
  if (lang && hljs.getLanguage(lang)) return hljs.highlight(code, { language: lang, ignoreIllegals: true }).value
  return code.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}
