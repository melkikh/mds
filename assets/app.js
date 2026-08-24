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

// not named escape: a top-level const here is a global lexical binding, and it would shadow
// window.escape for every other script on the page. Mermaid decodes a diagram by calling it,
// so naming it that turns every "-->" in a chart into "--&gt;" and the chart into a bomb.
const escapeHtml = text => text.replace(/[<>&"]/g, c => ({ '<': '&lt;', '>': '&gt;', '&': '&amp;', '"': '&quot;' }[c]))

const flash = (button, state) => {
  button.dataset[state] = ''
  setTimeout(() => delete button.dataset[state], 900)
}

const here = decodeURI(location.pathname)
let mounts = 1

const branch = nodes => nodes.map(node => {
  if (node.type === 'file') {
    return `<a href="${encodeURI('/' + node.path)}"${'/' + node.path === here ? ' class="here"' : ''}>` +
      `${escapeHtml(node.name)}</a>`
  }
  const isRoot = node.type === 'root'
  const drop = isRoot && mounts > 1
    ? `<button class="drop" title="Stop serving this" data-root="${escapeHtml(node.path)}">&#10005;</button>` : ''
  const hidden = mdsHidden().includes(node.path)
  const hide = `<button class="hide" data-dir="${escapeHtml(node.path)}"` +
    ` title="${hidden ? 'Show this by default' : 'Hide this until clicked'}">${hidden ? '&#9673;' : '&#9678;'}</button>`
  return `<details open${isRoot ? ' class="root"' : ''}><summary${hidden ? ' data-hidden' : ''}>` +
    `${escapeHtml(node.name)}${drop}${hide}</summary>${branch(node.children || [])}</details>`
}).join('')

const edit = document.getElementById('edit')
if (edit) {
  edit.onclick = async () => {
    if ((await post('/_edit?path=' + encodeURIComponent(edit.dataset.path))).ok) return
    flash(edit, 'failed')
  }
}

const copyFile = document.getElementById('copy-file')
if (copyFile) {
  copyFile.onclick = async () => {
    const source = fetch('/_raw?path=' + encodeURIComponent(copyFile.dataset.path))
      .then(response => response.ok ? response.text() : Promise.reject(response.status))
    try {
      // safari drops the user gesture across an await, so hand it the pending text instead
      if (window.ClipboardItem) {
        await navigator.clipboard.write([new ClipboardItem({
          'text/plain': source.then(text => new Blob([text], { type: 'text/plain' })),
        })])
      } else {
        await navigator.clipboard.writeText(await source)
      }
      flash(copyFile, 'done')
    } catch {
      flash(copyFile, 'failed')
    }
  }
}

const front = document.querySelector('details.frontmatter')
if (front) {
  front.open = localStorage.mdsFrontmatter === '1'
  front.ontoggle = () => front.open ? localStorage.mdsFrontmatter = '1' : delete localStorage.mdsFrontmatter
}

const veil = document.createElement('button')
veil.id = 'veil'
veil.innerHTML = '<svg viewBox="0 0 24 24" width="34" height="34" fill="none" stroke="currentColor" ' +
  'stroke-width="1.4" stroke-linecap="round"><path d="M2 12s4-6.5 10-6.5S22 12 22 12s-4 6.5-10 6.5S2 12 2 12Z"/>' +
  '<circle cx="12" cy="12" r="3"/><line x1="4" y1="20" x2="20" y2="4"/></svg>click to show'
document.body.append(veil)
veil.onclick = () => {
  delete sessionStorage.mdsAway
  delete root.dataset.concealed
  document.title = mdsTitle
}
addEventListener('blur', mdsConceal)
document.addEventListener('visibilitychange', () => document.hidden && mdsConceal())

// sitting on an uncovered page with the window in hand ends the trip away
const arrive = () => {
  if (!('concealed' in root.dataset) && document.hasFocus()) delete sessionStorage.mdsAway
}
addEventListener('focus', arrive)
arrive()

const content = document.querySelector('main')

content.querySelectorAll('pre:not(.mermaid)').forEach(pre => {
  if (pre.closest('.frontmatter')) return
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

// An id is made of the whole title, so a contents table written against the key a heading
// starts with — [R-0101](#r-0101) against "R-0101 — the rest of the sentence" — matches
// nothing and the browser stays where it is. Take the first heading the key names.
const reveal = fragment => {
  let wanted = fragment
  try {
    wanted = decodeURIComponent(fragment)
  } catch {
    // a fragment that is not valid escaping is still worth trying as it stands
  }
  if (!wanted || document.getElementById(wanted)) return
  const named = [...content.querySelectorAll('[id]')].find(node => node.id.startsWith(wanted + '-'))
  if (named) named.scrollIntoView()
}

addEventListener('hashchange', () => reveal(location.hash.slice(1)))
reveal(location.hash.slice(1))

content.onclick = event => {
  // the same link twice in a row is not a hashchange, so clicks are handled on their own
  const anchor = event.target.closest('a[href^="#"]')
  if (anchor) return reveal(anchor.getAttribute('href').slice(1))
  const code = event.target.closest('code')
  if (!code || code.closest('pre, a') || !getSelection().isCollapsed) return
  navigator.clipboard.writeText(code.textContent).then(() => {
    code.dataset.copied = ''
    setTimeout(() => delete code.dataset.copied, 900)
  })
}

const tree = document.getElementById('tree')
if (tree) {
  const files = document.getElementById('files')
  const showTree = async () => {
    try {
      const data = await (await fetch('/_tree')).json()
      mounts = (data.nodes || []).length
      files.innerHTML = branch(data.nodes || [])
    } catch {
      // the server went away; the tree already on the page beats an empty one
    }
  }
  const expand = () => {
    delete localStorage.mdsTree
    root.dataset.tree = ''
    showTree()
  }
  tree.onclick = event => {
    // collapsed, the whole rail is the way back in
    if (!('tree' in root.dataset)) return expand()
    if (event.target.closest('#tree-toggle')) {
      delete root.dataset.tree
      localStorage.mdsTree = 'closed'
      return
    }
    const drop = event.target.closest('.drop')
    if (drop) {
      event.preventDefault()
      post('/_drop?root=' + encodeURIComponent(drop.dataset.root))
      return
    }
    const hide = event.target.closest('.hide')
    if (!hide) return
    event.preventDefault()
    const dir = hide.dataset.dir
    const marked = mdsHidden().filter(known => known !== dir)
    if (marked.length === mdsHidden().length) marked.push(dir)
    localStorage.mdsHidden = JSON.stringify(marked)
    showTree()
  }
  if ('tree' in root.dataset) showTree()
}

if (window.mermaid) mermaid.initialize({ startOnLoad: true, theme: root.dataset.theme === 'dark' ? 'dark' : 'default' })
