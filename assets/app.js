const root = document.documentElement
const offline = () => 'offline' in root.dataset

const token = document.querySelector('meta[name=mds-token]').content
const post = url => fetch(url, { method: 'POST', headers: { 'X-Mds-Token': token } })

const events = new EventSource('/_events')
events.onmessage = event => {
  const [command, target] = event.data.split(' ')
  if (command === 'go' && target !== location.pathname) location.href = target
  else location.reload()
}
events.onerror = () => root.dataset.offline = ''
events.onopen = () => offline() && location.reload()

document.getElementById('theme-toggle').onclick = () => {
  const theme = root.dataset.theme === 'dark' ? 'light' : 'dark'
  root.dataset.theme = theme
  localStorage.mdsTheme = theme
  if (!offline() && document.querySelector('.mermaid')) location.reload()
}

const escape = text => text.replace(/[<>&]/g, c => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;' }[c]))

const branch = nodes => nodes.map(node => {
  if (node.type === 'file') return `<a href="${encodeURI('/' + node.path)}">${escape(node.name)}</a>`
  const isRoot = node.type === 'root'
  const drop = isRoot ? `<button class="drop" title="Stop serving this" data-root="${escape(node.path)}">&#10005;</button>` : ''
  return `<details open${isRoot ? ' class="root"' : ''}><summary>${escape(node.name)}${drop}</summary>` +
    `${branch(node.children || [])}</details>`
}).join('')

const edit = document.getElementById('edit')
if (edit) {
  edit.onclick = async () => {
    if ((await post('/_edit?path=' + encodeURIComponent(edit.dataset.path))).ok) return
    edit.dataset.failed = ''
    setTimeout(() => delete edit.dataset.failed, 900)
  }
}

const content = document.querySelector('main')

content.querySelectorAll('pre:not(.mermaid)').forEach(pre => {
  const block = document.createElement('div')
  const button = document.createElement('button')
  button.textContent = 'copy'
  button.onclick = () => navigator.clipboard.writeText(pre.textContent.replace(/\n$/, '')).then(() => {
    button.textContent = 'copied'
    setTimeout(() => button.textContent = 'copy', 900)
  })
  block.className = 'code-block'
  pre.replaceWith(block)
  block.append(pre, button)
})

content.onclick = event => {
  const code = event.target.closest('code')
  if (!code || code.closest('pre, a') || !getSelection().isCollapsed) return
  navigator.clipboard.writeText(code.textContent).then(() => {
    code.dataset.copied = ''
    setTimeout(() => delete code.dataset.copied, 900)
  })
}

const burger = document.getElementById('burger')
if (burger) {
  const tree = document.getElementById('tree')
  const showTree = async () => {
    const data = await (await fetch('/_tree')).json()
    tree.innerHTML = branch(data.nodes || [])
    tree.hidden = false
  }
  burger.onclick = () => {
    if (!tree.hidden) {
      delete localStorage.mdsTree
      tree.hidden = true
      return
    }
    localStorage.mdsTree = '1'
    showTree()
  }
  tree.onclick = event => {
    const drop = event.target.closest('.drop')
    if (!drop) return
    event.preventDefault()
    post('/_drop?root=' + encodeURIComponent(drop.dataset.root))
  }
  if (localStorage.mdsTree) showTree()
}

if (window.mermaid) mermaid.initialize({ startOnLoad: true, theme: root.dataset.theme === 'dark' ? 'dark' : 'default' })
