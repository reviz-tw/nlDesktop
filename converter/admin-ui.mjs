// Admin UI 的前端 bundle 入口：Tiptap 編輯器 + 關聯欄位 picker。
// 關鍵：extensions 與轉換服務、Go 白名單共用同一份 schema（schema.mjs），
// 編輯器產生的 doc 一定通過 server 端驗證。
//
// build：npm run build → ../admin/static/admin.js（committed，由 nl-server 內嵌服務）
import { Editor } from '@tiptap/core'
import { extensions } from './schema.mjs'

// ---- Rich text 編輯器 ----
// 容器約定：
//   <div class="rt" data-input="<textarea id>">…</div>
//   textarea 內容為 PM doc JSON（空字串 = 空文件），submit 前保持同步。

const TOOLBAR = [
  { label: 'B', title: '粗體', cmd: (e) => e.chain().focus().toggleBold().run(), active: (e) => e.isActive('bold') },
  { label: 'I', title: '斜體', cmd: (e) => e.chain().focus().toggleItalic().run(), active: (e) => e.isActive('italic') },
  { label: 'S', title: '刪除線', cmd: (e) => e.chain().focus().toggleStrike().run(), active: (e) => e.isActive('strike') },
  { label: 'H2', title: '標題 2', cmd: (e) => e.chain().focus().toggleHeading({ level: 2 }).run(), active: (e) => e.isActive('heading', { level: 2 }) },
  { label: 'H3', title: '標題 3', cmd: (e) => e.chain().focus().toggleHeading({ level: 3 }).run(), active: (e) => e.isActive('heading', { level: 3 }) },
  { label: '••', title: '項目清單', cmd: (e) => e.chain().focus().toggleBulletList().run(), active: (e) => e.isActive('bulletList') },
  { label: '1.', title: '編號清單', cmd: (e) => e.chain().focus().toggleOrderedList().run(), active: (e) => e.isActive('orderedList') },
  { label: '❝', title: '引用', cmd: (e) => e.chain().focus().toggleBlockquote().run(), active: (e) => e.isActive('blockquote') },
  {
    label: '🔗', title: '連結',
    cmd: (e) => {
      const prev = e.getAttributes('link').href || ''
      const url = window.prompt('連結網址（留空移除）', prev)
      if (url === null) return
      if (url === '') e.chain().focus().unsetLink().run()
      else e.chain().focus().setLink({ href: url }).run()
    },
    active: (e) => e.isActive('link'),
  },
  {
    label: '▶', title: 'YouTube',
    cmd: (e) => {
      const url = window.prompt('YouTube 網址')
      if (url) e.chain().focus().setYoutubeVideo({ src: url }).run()
    },
    active: () => false,
  },
]

function initEditor(container) {
  const input = document.getElementById(container.dataset.input)
  let content = null
  try {
    content = input.value.trim() ? JSON.parse(input.value) : null
  } catch {
    content = null
  }

  const bar = document.createElement('div')
  bar.className = 'rt-toolbar'
  const mount = document.createElement('div')
  mount.className = 'rt-body'
  container.append(bar, mount)

  const editor = new Editor({
    element: mount,
    extensions,
    editable: container.dataset.readonly !== 'true',
    editorProps: { attributes: { role: 'textbox', 'aria-label': container.dataset.label, 'aria-multiline': 'true' } },
    content,
    onUpdate({ editor }) {
      input.value = editor.isEmpty ? '' : JSON.stringify(editor.getJSON())
      input.dispatchEvent(new Event('input', { bubbles: true }))
      refresh()
    },
    onSelectionUpdate: () => refresh(),
  })

  const buttons = TOOLBAR.map((item) => {
    const b = document.createElement('button')
    b.type = 'button'
    b.textContent = item.label
    b.title = item.title
    b.setAttribute('aria-label', item.title)
    b.disabled = container.dataset.readonly === 'true'
    b.addEventListener('click', () => item.cmd(editor))
    bar.appendChild(b)
    return { b, item }
  })
  function refresh() {
    for (const { b, item } of buttons) { b.classList.toggle('on', item.active(editor)); b.setAttribute('aria-pressed', String(item.active(editor))) }
  }
  refresh()
}

// ---- 關聯欄位 picker ----
// 容器約定：
//   <div class="relpicker" data-name="tags" data-list="Tag" data-many="true">
//     <div class="chips"></div>
//     <input type="text" class="relsearch" placeholder="搜尋…">
//   </div>
// 現值以 <input type="hidden" name="tags" value="id"> 存於容器內（可多個），
// 與原本 multi-select 的表單協定相同。

function initPicker(container) {
  const name = container.dataset.name
  const list = container.dataset.list
  const many = container.dataset.many === 'true'
  const chips = container.querySelector('.chips')
  const search = container.querySelector('.relsearch')
  const results = document.createElement('div')
  results.className = 'relresults'
  container.appendChild(results)

  function ids() {
    return [...container.querySelectorAll(`input[name="${name}"]`)].map((i) => i.value)
  }

  function addChip(id, label) {
    if (ids().includes(String(id))) return
    if (!many) removeAll()
    const chip = document.createElement('span')
    chip.className = 'chip'
    chip.innerHTML = `${escapeHTML(label)} <button type="button" aria-label="移除">×</button>`
    const hidden = document.createElement('input')
    hidden.type = 'hidden'
    hidden.name = name
    hidden.value = id
    chip.querySelector('button').addEventListener('click', () => {
      chip.remove()
      hidden.remove()
      search.dispatchEvent(new Event('change', { bubbles: true }))
    })
    chips.appendChild(chip)
    container.appendChild(hidden)
  }

  function removeAll() {
    chips.innerHTML = ''
    container.querySelectorAll(`input[name="${name}"]`).forEach((i) => i.remove())
  }

  // 初始 chips（server 已渲染 data-selected JSON）
  try {
    for (const { id, label } of JSON.parse(container.dataset.selected || '[]')) addChip(id, label)
  } catch { /* noop */ }

  let timer = null
  let controller = null
  const close = () => { controller?.abort(); results.replaceChildren(); search.setAttribute('aria-expanded', 'false') }
  search.setAttribute('aria-expanded', 'false')
  async function lookup() {
    controller?.abort()
    controller = new AbortController()
    results.replaceChildren()
    try {
      const resp = await fetch(`/admin/api/options?list=${encodeURIComponent(list)}&q=${encodeURIComponent(search.value.trim())}`, { signal: controller.signal })
      if (!resp.ok || resp.redirected) throw new Error('無法載入項目，請確認登入狀態')
      const options = (await resp.json()).filter(opt => !ids().includes(String(opt.id)))
      search.setAttribute('aria-expanded', 'true')
      for (const opt of options) {
        const row = document.createElement('button')
        row.type = 'button'; row.className = 'relopt'; row.textContent = opt.label
        row.addEventListener('click', () => {
          addChip(opt.id, opt.label); search.value = ''; close()
          search.dispatchEvent(new Event('change', { bubbles: true }))
          search.focus(); close()
        })
        results.appendChild(row)
      }
      if (!options.length) results.innerHTML = '<div class="relempty">沒有符合的項目</div>'
    } catch (err) {
      if (err.name !== 'AbortError') { const message = document.createElement('div'); message.className = 'relempty'; message.textContent = '無法載入項目，請稍後再試'; results.replaceChildren(message) }
    }
  }
  search.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(lookup, 200) })
  search.addEventListener('focus', lookup)
  container.addEventListener('focusout', e => { if (!container.contains(e.relatedTarget)) {clearTimeout(timer);close()} })
  container.addEventListener('keydown', e => {
    if (e.key === 'Escape') {close();search.focus();close()}
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      const options = [...results.querySelectorAll('button')]
      if (!options.length) return
      e.preventDefault()
      const i = options.indexOf(document.activeElement)
      const next = e.key === 'ArrowDown' ? (i + 1) % options.length : (i - 1 + options.length) % options.length
      options[next].focus()
    }
  })
}

function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]))
}

export function init() {
  document.querySelectorAll('.rt').forEach(initEditor)
  document.querySelectorAll('.relpicker').forEach(initPicker)
  initPage()
}

function initPage() {
  if (matchMedia('(max-width:760px)').matches) document.querySelector('.nav-item[aria-current=page]')?.scrollIntoView({ block: 'nearest', inline: 'center' })
  const controls = document.querySelector('.list-controls')
  controls?.querySelectorAll('[name="filter"]').forEach(input => input.addEventListener('change', () => controls.requestSubmit()))
  const bulk = document.querySelector('.bulk-form')
  if (bulk) {
    const boxes = [...bulk.querySelectorAll('[name="ids"]')]
    const all = bulk.querySelector('[data-select-all]')
    const bar = bulk.querySelector('.bulk-bar')
    function sync() {
      const count = boxes.filter(x => x.checked).length
      if (bar) {bar.hidden = count === 0; bar.querySelector('[data-selected-count]').textContent = count}
      if (all) {all.checked = count > 0 && count === boxes.length; all.indeterminate = count > 0 && count < boxes.length}
    }
    boxes.forEach(x => x.addEventListener('change', sync))
    all?.addEventListener('change', () => {boxes.forEach(x => {x.checked = all.checked});sync()})
    bulk.querySelector('.clear-selection')?.addEventListener('click', () => {boxes.forEach(x => {x.checked = false});sync()})
    bulk.addEventListener('submit', e => {
      const count = boxes.filter(x => x.checked).length
      if (!count) {e.preventDefault();return}
      const action = e.submitter?.value
      if (['delete','published'].includes(action) && !confirm(action === 'delete' ? `確定刪除 ${count} 筆資料？此操作無法復原。` : `確定發佈 ${count} 篇文章？尚未設定發佈時間的文章將使用目前時間。`)) e.preventDefault()
    })
  }
  const form = document.querySelector('form.item')
  if (!form) return
  const status = form.querySelector('[data-save-status]')
  let dirty = false
  let submitting = false
  const changed = () => {dirty = true;status.textContent = '尚有未儲存的變更'}
  form.addEventListener('input', changed)
  form.addEventListener('change', changed)
  form.addEventListener('submit', () => {submitting = true;status.textContent = '儲存中…'})
  window.addEventListener('beforeunload', e => {if (dirty && !submitting) {e.preventDefault();e.returnValue = ''}})
  form.querySelectorAll('select').forEach(select => select.addEventListener('change', () => {
    const badge = select.parentElement.querySelector('[data-state-badge]')
    if (badge) {badge.textContent = select.selectedOptions[0].textContent;badge.className = `status-pill ${select.value}`}
  }))
  let expanded = null
  const collapse = () => {
    if (!expanded) return
    expanded.closest('.rich-field').classList.remove('fullscreen')
    expanded.textContent = '⛶ 展開全螢幕';expanded.setAttribute('aria-expanded', 'false')
    expanded.focus();expanded = null
  }
  form.querySelectorAll('[data-expand]').forEach(button => {
    button.setAttribute('aria-expanded', 'false')
    button.addEventListener('click', () => {
      if (expanded === button) {collapse();return}
      collapse();expanded = button
      button.closest('.rich-field').classList.add('fullscreen')
      button.textContent = '× 關閉全螢幕';button.setAttribute('aria-expanded', 'true')
    })
  })
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape' && expanded) {e.preventDefault();collapse()}
    if (e.key === 'Tab' && expanded) {
      const focusable = [...expanded.closest('.rich-field').querySelectorAll('button:not([disabled]), [contenteditable="true"]')]
      const first = focusable[0], last = focusable[focusable.length-1]
      if (e.shiftKey && document.activeElement === first) {e.preventDefault();last.focus()}
      else if (!e.shiftKey && document.activeElement === last) {e.preventDefault();first.focus()}
    }
  })
}

init()
