const root = document.documentElement
const offline = () => 'offline' in root.dataset

const events = new EventSource('/_events')
events.onmessage = () => location.reload()
events.onerror = () => root.dataset.offline = ''
events.onopen = () => offline() && location.reload()

document.getElementById('theme-toggle').onclick = () => {
  const theme = root.dataset.theme === 'dark' ? 'light' : 'dark'
  root.dataset.theme = theme
  localStorage.mdsTheme = theme
  if (!offline() && document.querySelector('.mermaid')) location.reload()
}

const escape = text => text.replace(/[<>&]/g, c => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;' }[c]))

const branch = nodes => nodes.map(node => node.type === 'dir'
  ? `<details open><summary>${escape(node.name)}</summary>${branch(node.children || [])}</details>`
  : `<a href="${encodeURI('/' + node.path)}">${escape(node.name)}</a>`).join('')

const burger = document.getElementById('burger')
if (burger) {
  const tree = document.getElementById('tree')
  burger.onclick = async () => {
    if (!tree.innerHTML) {
      const data = await (await fetch('/_tree')).json()
      tree.innerHTML = branch(data.nodes || [])
    }
    tree.hidden = !tree.hidden
  }
}

mermaid.initialize({ startOnLoad: true, theme: root.dataset.theme === 'dark' ? 'dark' : 'default' })
