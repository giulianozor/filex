'use strict';

// ─── State ────────────────────────────────────────────────────────────────────
const state = {
  currentPath: '/',
  showDotfiles: false,
  sortBy: 'name',
  sortDir: 'asc',
  selectedFiles: new Set(),
  entries: [],
  moveSrc: null,
  copySrc: null,
  editorPath: null,
  favourites: [],
  opAbort: null,   // function to cancel the current running operation
  previewList: [],  // previewable file entries in the current directory
  previewIndex: -1, // index of the currently previewed entry in previewList
};

// ─── Icons ────────────────────────────────────────────────────────────────────
const ICONS = {
  dir: `<svg class="file-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#4a9eff" stroke-width="2"><path d="M3 3h7l2 3h9a1 1 0 0 1 1 1v13a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1z"/></svg>`,
  image: `<svg class="file-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#4caf50" stroke-width="2"><rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.5"/><polyline points="21 15 16 10 5 21"/></svg>`,
  video: `<svg class="file-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#9c27b0" stroke-width="2"><polygon points="23 7 16 12 23 17 23 7"/><rect x="1" y="5" width="15" height="14" rx="2"/></svg>`,
  audio: `<svg class="file-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#ff9800" stroke-width="2"><path d="M9 18V5l12-2v13"/><circle cx="6" cy="18" r="3"/><circle cx="18" cy="16" r="3"/></svg>`,
  pdf:   `<svg class="file-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#f44336" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/><polyline points="10 9 9 9 8 9"/></svg>`,
  archive:`<svg class="file-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#795548" stroke-width="2"><polyline points="21 8 21 21 3 21 3 8"/><rect x="1" y="3" width="22" height="5"/><line x1="10" y1="12" x2="14" y2="12"/></svg>`,
  code:  `<svg class="file-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#00bcd4" stroke-width="2"><polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/></svg>`,
  text:  `<svg class="file-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#9e9e9e" stroke-width="2"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><line x1="16" y1="13" x2="8" y2="13"/><line x1="16" y1="17" x2="8" y2="17"/></svg>`,
  file:  `<svg class="file-icon" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="#757575" stroke-width="2"><path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><polyline points="13 2 13 9 20 9"/></svg>`,
  starFilled: `<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" stroke="currentColor" stroke-width="2"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>`,
  starEmpty:  `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/></svg>`,
};

function getIcon(entry) {
  return ICONS[entry.mime_hint] || ICONS.file;
}

// ─── Utilities ────────────────────────────────────────────────────────────────
function formatSize(bytes) {
  if (bytes == null || bytes === 0) return '—';
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  let v = bytes;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return (i === 0 ? v : v.toFixed(1)) + ' ' + units[i];
}

function formatDate(isoStr) {
  if (!isoStr) return '—';
  const d = new Date(isoStr);
  return d.toLocaleDateString() + ' ' + d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
}

function joinPath(...parts) {
  let p = parts.join('/').replace(/\/+/g, '/');
  if (!p.startsWith('/')) p = '/' + p;
  return p;
}

function basename(p) {
  return p.replace(/\/$/, '').split('/').pop() || '/';
}

// ─── Toast ────────────────────────────────────────────────────────────────────
function toast(msg, type = 'info') {
  const c = document.getElementById('toast-container');
  const el = document.createElement('div');
  el.className = 'toast ' + (type === 'error' ? 'error' : type === 'success' ? 'success' : '');
  el.textContent = msg;
  c.appendChild(el);
  setTimeout(() => el.remove(), 3500);
}

// ─── API helpers ──────────────────────────────────────────────────────────────
async function apiGet(url) {
  const r = await fetch(url);
  if (r.status === 401) { window.location.href = '/login'; return null; }
  const data = await r.json();
  if (!r.ok) throw new Error(data.error || r.statusText);
  return data;
}

async function apiPost(url, body, opts = {}) {
  const r = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal: opts.signal,
  });
  if (r.status === 401) { window.location.href = '/login'; return null; }
  const data = await r.json();
  if (!r.ok) throw new Error(data.error || r.statusText);
  return data;
}

// ─── Directory loading ────────────────────────────────────────────────────────
async function loadDirectory(path) {
  path = path || '/';
  state.currentPath = path;
  state.selectedFiles.clear();
  updateSelectionButtons();

  try {
    const params = new URLSearchParams({ path, dotfiles: state.showDotfiles });
    const entries = await apiGet('/api/list?' + params);
    state.entries = entries || [];
    renderBreadcrumb();
    renderFileList();
    loadDiskInfo();
    updateUrl(path);
  } catch (e) {
    toast('Error loading directory: ' + e.message, 'error');
  }
}

function updateUrl(path) {
  const url = new URL(window.location);
  url.searchParams.set('path', path);
  history.pushState({ path }, '', url);
}

// ─── Breadcrumb ───────────────────────────────────────────────────────────────
function renderBreadcrumb() {
  const bc = document.getElementById('breadcrumb');
  bc.innerHTML = '';
  const parts = state.currentPath.split('/').filter(Boolean);

  const home = document.createElement('span');
  home.className = 'bc-part' + (parts.length === 0 ? ' current' : '');
  home.textContent = '⌂ Root';
  home.onclick = () => loadDirectory('/');
  bc.appendChild(home);

  let cumPath = '';
  parts.forEach((p, i) => {
    cumPath += '/' + p;
    const sep = document.createElement('span');
    sep.className = 'bc-sep';
    sep.textContent = '/';
    bc.appendChild(sep);

    const part = document.createElement('span');
    const isLast = i === parts.length - 1;
    part.className = 'bc-part' + (isLast ? ' current' : '');
    part.textContent = p;
    const cp = cumPath;
    part.onclick = () => { if (!isLast) loadDirectory(cp); };
    bc.appendChild(part);
  });
}

// ─── File list rendering ──────────────────────────────────────────────────────
function getSortedEntries() {
  const entries = [...state.entries];
  const { sortBy, sortDir } = state;

  entries.sort((a, b) => {
    // Dirs always first
    if (a.is_dir !== b.is_dir) return a.is_dir ? -1 : 1;

    let va, vb;
    if (sortBy === 'size') {
      va = a.size; vb = b.size;
    } else if (sortBy === 'date') {
      va = new Date(a.mod_time).getTime();
      vb = new Date(b.mod_time).getTime();
    } else {
      va = a.name.toLowerCase(); vb = b.name.toLowerCase();
    }
    if (va < vb) return sortDir === 'asc' ? -1 : 1;
    if (va > vb) return sortDir === 'asc' ? 1 : -1;
    return 0;
  });
  return entries;
}

function renderFileList() {
  const tbody = document.getElementById('file-body');
  const empty = document.getElementById('empty-state');
  const entries = getSortedEntries();

  // Update sort arrows
  ['name', 'size', 'date'].forEach(col => {
    const el = document.getElementById('sort-' + col);
    if (el) {
      el.textContent = state.sortBy === col ? (state.sortDir === 'asc' ? ' ▲' : ' ▼') : '';
    }
  });
  document.querySelectorAll('.file-table th[data-sort]').forEach(th => {
    th.classList.toggle('sorted', th.dataset.sort === state.sortBy);
  });

  tbody.innerHTML = '';

  if (entries.length === 0) {
    empty.style.display = 'block';
    return;
  }
  empty.style.display = 'none';

  entries.forEach(entry => {
    const tr = document.createElement('tr');
    tr.dataset.path = entry.path;
    if (state.selectedFiles.has(entry.path)) tr.classList.add('selected');

    // ── Long-press to select (touch devices) ─────────────────────────────────
    let _longPressTimer = null;
    let _longPressStartX = 0, _longPressStartY = 0;
    tr.addEventListener('touchstart', e => {
      if (e.target.closest('button, input, a')) return;
      const t = e.touches[0];
      _longPressStartX = t.clientX; _longPressStartY = t.clientY;
      tr.classList.add('long-press-active');
      _longPressTimer = setTimeout(() => {
        _longPressTimer = null;
        tr.classList.remove('long-press-active');
        const nowSelected = !state.selectedFiles.has(entry.path);
        cb.checked = nowSelected;
        toggleSelect(entry.path, nowSelected, tr);
        if (navigator.vibrate) navigator.vibrate(30);
      }, 500);
    }, { passive: true });
    tr.addEventListener('touchmove', e => {
      if (_longPressTimer) {
        const t = e.touches[0];
        if (Math.abs(t.clientX - _longPressStartX) > 8 || Math.abs(t.clientY - _longPressStartY) > 8) {
          clearTimeout(_longPressTimer); _longPressTimer = null;
          tr.classList.remove('long-press-active');
        }
      }
    }, { passive: true });
    const _lpCancel = () => {
      if (_longPressTimer) { clearTimeout(_longPressTimer); _longPressTimer = null; }
      tr.classList.remove('long-press-active');
    };
    tr.addEventListener('touchend', _lpCancel, { passive: true });
    tr.addEventListener('touchcancel', _lpCancel, { passive: true });
    // ─────────────────────────────────────────────────────────────────────────

    // Checkbox
    const tdCheck = document.createElement('td');
    const cb = document.createElement('input');
    cb.type = 'checkbox';
    cb.className = 'row-check';
    cb.checked = state.selectedFiles.has(entry.path);
    cb.onchange = () => toggleSelect(entry.path, cb.checked, tr);
    tdCheck.appendChild(cb);
    tr.appendChild(tdCheck);

    // Name
    const tdName = document.createElement('td');
    const nameCell = document.createElement('div');
    nameCell.className = 'file-name-cell';
    nameCell.innerHTML = getIcon(entry);

    const nameSpan = document.createElement('span');
    nameSpan.className = 'file-name' + (entry.is_dir ? ' is-dir' : '');
    nameSpan.textContent = entry.name;
    if (entry.is_dir) {
      nameSpan.onclick = () => loadDirectory(entry.path);
    } else {
      nameSpan.onclick = () => openPreview(entry);
    }
    nameCell.appendChild(nameSpan);
    tdName.appendChild(nameCell);
    tr.appendChild(tdName);

    // Size
    const tdSize = document.createElement('td');
    tdSize.className = 'file-size';
    tdSize.textContent = entry.is_dir ? '—' : formatSize(entry.size);
    tr.appendChild(tdSize);

    // Date
    const tdDate = document.createElement('td');
    tdDate.className = 'file-date';
    tdDate.textContent = formatDate(entry.mod_time);
    tr.appendChild(tdDate);

    // Actions
    const tdAct = document.createElement('td');
    const actions = document.createElement('div');
    actions.className = 'actions';

    if (!entry.is_dir) {
      actions.appendChild(makeIconBtn(
        `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>`,
        'Download',
        () => downloadFile(entry.path)
      ));

      const isEditable = ['text','code','file'].includes(entry.mime_hint);
      if (isEditable) {
        actions.appendChild(makeIconBtn(
          `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>`,
          'Edit',
          () => openEditor(entry)
        ));
      }

      actions.appendChild(makeIconBtn(
        `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="15 10 20 15 15 20"/><path d="M4 4v7a4 4 0 0 0 4 4h12"/></svg>`,
        'Move',
        () => openMoveModal(entry.path)
      ));

      actions.appendChild(makeIconBtn(
        `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`,
        'Copy',
        () => openCopyModal(entry.path)
      ));
    }

    if (entry.is_dir) {
      actions.appendChild(makeIconBtn(
        `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>`,
        'Folder info',
        (e) => showFolderInfoPopup(entry, e.currentTarget)
      ));

      const isFav = isFavourite(entry.path);
      const starBtn = makeIconBtn(
        isFav ? ICONS.starFilled : ICONS.starEmpty,
        isFav ? 'Remove from Favourites' : 'Add to Favourites',
        () => toggleFavourite(entry.path, entry.name, starBtn)
      );
      if (isFav) starBtn.classList.add('fav-active');
      actions.appendChild(starBtn);

      actions.appendChild(makeIconBtn(
        `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>`,
        'Download as zip',
        () => downloadZip([entry.path])
      ));

      actions.appendChild(makeIconBtn(
        `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="15 10 20 15 15 20"/><path d="M4 4v7a4 4 0 0 0 4 4h12"/></svg>`,
        'Move',
        () => openMoveModal(entry.path)
      ));

      actions.appendChild(makeIconBtn(
        `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>`,
        'Copy',
        () => openCopyModal(entry.path)
      ));
    }

    actions.appendChild(makeIconBtn(
      `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"/><path d="M8 5h8"/><path d="M8 19h8"/></svg>`,
      'Rename',
      () => startInlineRename(entry, nameSpan)
    ));

    const delBtn = makeIconBtn(
      `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4h6v2"/></svg>`,
      'Delete',
      () => deleteFiles([entry.path])
    );
    delBtn.classList.add('danger');
    actions.appendChild(delBtn);

    tdAct.appendChild(actions);
    tr.appendChild(tdAct);
    tbody.appendChild(tr);
  });

  // Update select-all
  const selectAll = document.getElementById('select-all');
  if (selectAll) {
    selectAll.checked = entries.length > 0 && entries.every(e => state.selectedFiles.has(e.path));
    selectAll.indeterminate = !selectAll.checked && entries.some(e => state.selectedFiles.has(e.path));
  }
}

function makeIconBtn(svgStr, title, onclick) {
  const btn = document.createElement('button');
  btn.className = 'icon-btn';
  btn.title = title;
  btn.innerHTML = svgStr;
  btn.addEventListener('click', onclick);
  return btn;
}

// ─── Selection ────────────────────────────────────────────────────────────────
function toggleSelect(path, checked, tr) {
  if (checked) state.selectedFiles.add(path);
  else state.selectedFiles.delete(path);
  tr.classList.toggle('selected', checked);
  updateSelectionButtons();
  updateSelectAllState();
}

function updateSelectionButtons() {
  const btn = document.getElementById('btn-delete-sel');
  if (btn) btn.style.display = state.selectedFiles.size > 0 ? '' : 'none';
  const dlBtn = document.getElementById('btn-download-sel');
  if (dlBtn) dlBtn.style.display = state.selectedFiles.size > 0 ? '' : 'none';
  const mvBtn = document.getElementById('btn-move-sel');
  if (mvBtn) mvBtn.style.display = state.selectedFiles.size > 0 ? '' : 'none';
  const cpBtn = document.getElementById('btn-copy-sel');
  if (cpBtn) cpBtn.style.display = state.selectedFiles.size > 0 ? '' : 'none';
}

function updateSelectAllState() {
  const selectAll = document.getElementById('select-all');
  if (!selectAll) return;
  const count = state.entries.length;
  const sel = state.entries.filter(e => state.selectedFiles.has(e.path)).length;
  selectAll.checked = count > 0 && sel === count;
  selectAll.indeterminate = sel > 0 && sel < count;
}

// ─── Sort ─────────────────────────────────────────────────────────────────────
function setSort(col) {
  if (state.sortBy === col) {
    state.sortDir = state.sortDir === 'asc' ? 'desc' : 'asc';
  } else {
    state.sortBy = col;
    state.sortDir = 'asc';
  }
  renderFileList();
}

// ─── Inline rename ────────────────────────────────────────────────────────────
function startInlineRename(entry, nameSpan) {
  const oldName = entry.name;
  const input = document.createElement('input');
  input.type = 'text';
  input.className = 'rename-input';
  input.value = oldName;
  nameSpan.replaceWith(input);
  input.focus();
  input.select();

  async function commit() {
    const newName = input.value.trim();
    if (!newName || newName === oldName) {
      input.replaceWith(nameSpan);
      return;
    }
    try {
      await apiPost('/api/rename', { path: entry.path, newname: newName });
      toast('Renamed to ' + newName, 'success');
      loadDirectory(state.currentPath);
    } catch (e) {
      toast('Rename failed: ' + e.message, 'error');
      input.replaceWith(nameSpan);
    }
  }

  input.onblur = commit;
  input.onkeydown = e => {
    if (e.key === 'Enter') { e.preventDefault(); input.blur(); }
    if (e.key === 'Escape') { input.replaceWith(nameSpan); }
  };
}

// ─── Delete ───────────────────────────────────────────────────────────────────
async function deleteFiles(paths) {
  if (!paths || paths.length === 0) return;
  const names = paths.map(basename).join(', ');
  if (!confirm(`Delete ${names}?\nThis cannot be undone.`)) return;
  try {
    await apiPost('/api/delete', { paths });
    toast(paths.length === 1 ? `Deleted ${names}` : `Deleted ${paths.length} items`, 'success');
    paths.forEach(p => state.selectedFiles.delete(p));
    updateSelectionButtons();
    loadDirectory(state.currentPath);
  } catch (e) {
    toast('Delete failed: ' + e.message, 'error');
  }
}

// ─── Download ─────────────────────────────────────────────────────────────────
function downloadFile(path) {
  const a = document.createElement('a');
  a.href = '/api/download?path=' + encodeURIComponent(path);
  a.download = basename(path);
  document.body.appendChild(a);
  a.click();
  a.remove();
}

// ─── Operation progress (sidebar) ────────────────────────────────────────────
function showOpProgress(label, pct, sub) {
  const el = document.getElementById('op-progress');
  if (!el) return;
  el.style.display = 'block';
  document.getElementById('op-progress-label').textContent = label;
  const fill = document.getElementById('op-progress-fill');
  if (pct == null) {
    fill.classList.add('indeterminate');
    fill.style.width = '';
  } else {
    fill.classList.remove('indeterminate');
    fill.style.width = pct + '%';
  }
  document.getElementById('op-progress-sub').textContent = sub || '';
}

function hideOpProgress() {
  const el = document.getElementById('op-progress');
  if (el) el.style.display = 'none';
  state.opAbort = null;
}

// ─── Upload ───────────────────────────────────────────────────────────────────

// Sentinel error thrown when an XHR upload is aborted by the user.
class UploadAbortError extends Error {
  constructor() { super('Upload cancelled'); this.name = 'UploadAbortError'; }
}

async function uploadFiles(files) {
  if (!files || files.length === 0) return;
  const bar = document.getElementById('upload-progress-bar');
  bar.style.display = 'block';
  bar.style.width = '0%';

  const total = files.length;
  let done = 0;
  let cancelled = false;
  let currentXhr = null;

  state.opAbort = () => {
    cancelled = true;
    if (currentXhr) currentXhr.abort();
  };
  showOpProgress('Uploading…', 0, `0 / ${total} file${total !== 1 ? 's' : ''}`);

  for (const file of files) {
    if (cancelled) break;
    const fd = new FormData();
    fd.append('file', file);
    try {
      await new Promise((resolve, reject) => {
        const xhr = new XMLHttpRequest();
        currentXhr = xhr;
        xhr.open('POST', '/api/upload?path=' + encodeURIComponent(state.currentPath));
        xhr.upload.onprogress = e => {
          if (e.lengthComputable) {
            const pct = ((done + e.loaded / e.total) / total) * 100;
            bar.style.width = pct + '%';
            showOpProgress('Uploading…', pct, file.name);
          }
        };
        xhr.onload = () => {
          if (xhr.status === 200) resolve();
          else {
            try { reject(new Error(JSON.parse(xhr.responseText).error)); }
            catch { reject(new Error(xhr.statusText)); }
          }
        };
        xhr.onerror = () => reject(new Error('Network error'));
        xhr.onabort = () => reject(new UploadAbortError());
        xhr.send(fd);
      });
      done++;
      const pct = (done / total) * 100;
      bar.style.width = pct + '%';
      showOpProgress('Uploading…', pct, `${done} / ${total} file${total !== 1 ? 's' : ''}`);
    } catch (e) {
      if (e instanceof UploadAbortError || cancelled) break;
      toast('Upload failed: ' + e.message, 'error');
    } finally {
      currentXhr = null;
    }
  }

  hideOpProgress();
  bar.style.width = '100%';
  setTimeout(() => { bar.style.display = 'none'; bar.style.width = '0%'; }, 600);
  if (cancelled) {
    toast('Upload cancelled', 'info');
  } else if (done > 0) {
    toast(done + ' file' + (done === 1 ? '' : 's') + ' uploaded', 'success');
  }
  loadDirectory(state.currentPath);
}

// ─── Mkdir ────────────────────────────────────────────────────────────────────
function openMkdirModal() {
  document.getElementById('mkdir-name').value = '';
  openModal('modal-mkdir');
  setTimeout(() => document.getElementById('mkdir-name').focus(), 50);
}

async function doMkdir() {
  const name = document.getElementById('mkdir-name').value.trim();
  if (!name) { toast('Enter a folder name', 'error'); return; }
  try {
    await apiPost('/api/mkdir', { path: state.currentPath, name });
    toast('Created folder: ' + name, 'success');
    closeModal('modal-mkdir');
    loadDirectory(state.currentPath);
  } catch (e) {
    toast('Error: ' + e.message, 'error');
  }
}

// ─── Move ─────────────────────────────────────────────────────────────────────

// Per-modal browsed path
const folderBrowserPath = { move: '/', copy: '/' };

// Renders favourite folder chips inside a move/copy modal so the user can
// jump to a favourite destination with a single click.
function renderModalFavourites(mode) {
  const container = document.getElementById(mode + '-favs');
  const list = document.getElementById(mode + '-favs-list');
  const favs = state.favourites || [];
  if (favs.length === 0) {
    container.style.display = 'none';
    return;
  }
  container.style.display = 'block';
  list.innerHTML = '';
  favs.forEach(fav => {
    const chip = document.createElement('button');
    chip.type = 'button';
    chip.className = 'modal-fav-chip';
    chip.title = fav.path;
    const iconEl = document.createElement('span');
    iconEl.innerHTML = ICONS.dir; // static constant — safe
    chip.appendChild(iconEl);
    const nameSpan = document.createElement('span');
    nameSpan.textContent = fav.name;
    chip.appendChild(nameSpan);
    chip.onclick = () => {
      document.getElementById(mode + '-dst').value = fav.path;
      loadFolderBrowser(mode, fav.path);
    };
    list.appendChild(chip);
  });
}

async function loadFolderBrowser(mode, path) {
  folderBrowserPath[mode] = path;
  const listEl  = document.getElementById(mode + '-browser-list');
  const crumbEl = document.getElementById(mode + '-browser-crumb');
  const upBtn   = document.getElementById(mode + '-browser-up');

  crumbEl.textContent = path;
  upBtn.disabled = (path === '/');

  listEl.innerHTML = '<div class="folder-browser-empty">Loading…</div>';
  try {
    const params  = new URLSearchParams({ path, dotfiles: state.showDotfiles });
    const entries = await apiGet('/api/list?' + params);
    listEl.innerHTML = '';

    const dirs = (entries || []).filter(e => e.is_dir);
    if (dirs.length === 0) {
      listEl.innerHTML = '<div class="folder-browser-empty">No subfolders</div>';
      return;
    }

    dirs.forEach(dir => {
      const item = document.createElement('div');
      item.className = 'folder-browser-item';
      const iconEl = document.createElement('span');
      iconEl.innerHTML = ICONS.dir; // static constant — safe
      item.appendChild(iconEl);
      const nameSpan = document.createElement('span');
      nameSpan.textContent = dir.name;
      item.appendChild(nameSpan);
      item.onclick = () => {
        document.getElementById(mode + '-dst').value = dir.path;
        loadFolderBrowser(mode, dir.path);
      };
      listEl.appendChild(item);
    });
  } catch (e) {
    listEl.innerHTML = '<div class="folder-browser-empty" style="color:var(--danger)">Error loading folders</div>';
  }
}

function folderBrowserUp(mode) {
  const cur = folderBrowserPath[mode];
  if (cur === '/') return;
  const parts  = cur.split('/').filter(Boolean);
  parts.pop();
  const parent = parts.length === 0 ? '/' : '/' + parts.join('/');
  document.getElementById(mode + '-dst').value = parent;
  loadFolderBrowser(mode, parent);
}

function openMoveModal(pathOrPaths) {
  state.moveSrc = Array.isArray(pathOrPaths) ? pathOrPaths : [pathOrPaths];
  const initPath = state.currentPath;
  document.getElementById('move-dst').value = initPath;
  renderModalFavourites('move');
  openModal('modal-move');
  loadFolderBrowser('move', initPath);
  setTimeout(() => document.getElementById('move-dst').focus(), 50);
}

async function doMove() {
  const dst = document.getElementById('move-dst').value.trim();
  if (!dst) { toast('Enter a destination path', 'error'); return; }
  const srcs = Array.isArray(state.moveSrc) ? state.moveSrc : [state.moveSrc];
  const bar = document.getElementById('upload-progress-bar');
  const btn = document.getElementById('move-confirm');
  bar.classList.add('indeterminate');
  btn.disabled = true;

  const controller = new AbortController();
  state.opAbort = () => controller.abort();
  const sub0 = srcs.length > 1 ? `0 / ${srcs.length} items` : basename(srcs[0]);
  showOpProgress('Moving…', null, sub0);

  let done = 0;
  try {
    for (const src of srcs) {
      await apiPost('/api/move', { src, dst }, { signal: controller.signal });
      done++;
      showOpProgress('Moving…', (done / srcs.length) * 100,
        srcs.length > 1 ? `${done} / ${srcs.length} items` : basename(src));
    }
    toast(srcs.length === 1 ? 'Moved successfully' : `Moved ${srcs.length} items`, 'success');
    srcs.forEach(p => state.selectedFiles.delete(p));
    updateSelectionButtons();
    closeModal('modal-move');
    loadDirectory(state.currentPath);
  } catch (e) {
    if (e.name === 'AbortError') {
      toast('Move cancelled', 'info');
    } else {
      toast('Move failed: ' + e.message, 'error');
    }
  } finally {
    hideOpProgress();
    bar.classList.remove('indeterminate');
    bar.style.display = 'none';
    btn.disabled = false;
  }
}

// ─── Zip Download ─────────────────────────────────────────────────────────────
async function downloadZip(paths) {
  const bar = document.getElementById('upload-progress-bar');
  bar.classList.add('indeterminate');
  try {
    const name = paths.length === 1 ? basename(paths[0]) + '.zip' : 'download.zip';
    const r = await fetch('/api/zip-download', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ paths }),
    });
    if (r.status === 401) { window.location.href = '/login'; return; }
    if (!r.ok) {
      const data = await r.json().catch(() => ({}));
      throw new Error(data.error || r.statusText);
    }
    const blob = await r.blob();
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = name;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  } catch (e) {
    toast('Download failed: ' + e.message, 'error');
  } finally {
    bar.classList.remove('indeterminate');
    bar.style.display = 'none';
  }
}

// ─── Copy ─────────────────────────────────────────────────────────────────────
function openCopyModal(pathOrPaths) {
  state.copySrc = Array.isArray(pathOrPaths) ? pathOrPaths : [pathOrPaths];
  const initPath = state.currentPath;
  document.getElementById('copy-dst').value = initPath;
  renderModalFavourites('copy');
  openModal('modal-copy');
  loadFolderBrowser('copy', initPath);
  setTimeout(() => document.getElementById('copy-dst').focus(), 50);
}

async function doCopy() {
  const dst = document.getElementById('copy-dst').value.trim();
  if (!dst) { toast('Enter a destination path', 'error'); return; }
  const srcs = Array.isArray(state.copySrc) ? state.copySrc : [state.copySrc];
  const bar = document.getElementById('upload-progress-bar');
  const btn = document.getElementById('copy-confirm');
  bar.classList.add('indeterminate');
  btn.disabled = true;

  const controller = new AbortController();
  state.opAbort = () => controller.abort();
  const sub0 = srcs.length > 1 ? `0 / ${srcs.length} items` : basename(srcs[0]);
  showOpProgress('Copying…', null, sub0);

  let done = 0;
  try {
    for (const src of srcs) {
      await apiPost('/api/copy', { src, dst }, { signal: controller.signal });
      done++;
      showOpProgress('Copying…', (done / srcs.length) * 100,
        srcs.length > 1 ? `${done} / ${srcs.length} items` : basename(src));
    }
    toast(srcs.length === 1 ? 'Copied successfully' : `Copied ${srcs.length} items`, 'success');
    srcs.forEach(p => state.selectedFiles.delete(p));
    updateSelectionButtons();
    closeModal('modal-copy');
    loadDirectory(state.currentPath);
  } catch (e) {
    if (e.name === 'AbortError') {
      toast('Copy cancelled', 'info');
    } else {
      toast('Copy failed: ' + e.message, 'error');
    }
  } finally {
    hideOpProgress();
    bar.classList.remove('indeterminate');
    bar.style.display = 'none';
    btn.disabled = false;
  }
}

// ─── Text Editor ──────────────────────────────────────────────────────────────
async function openEditor(entry) {
  state.editorPath = entry.path;
  document.getElementById('editor-title').textContent = 'Edit — ' + entry.name;
  document.getElementById('editor-textarea').value = 'Loading…';
  openModal('modal-editor');
  try {
    const data = await apiGet('/api/read?path=' + encodeURIComponent(entry.path));
    document.getElementById('editor-textarea').value = data.content;
  } catch (e) {
    document.getElementById('editor-textarea').value = '';
    toast('Could not read file: ' + e.message, 'error');
  }
}

async function saveEditor() {
  const content = document.getElementById('editor-textarea').value;
  try {
    await apiPost('/api/write', { path: state.editorPath, content });
    toast('Saved', 'success');
    closeModal('modal-editor');
    loadDirectory(state.currentPath);
  } catch (e) {
    toast('Save failed: ' + e.message, 'error');
  }
}

// ─── Preview ──────────────────────────────────────────────────────────────────
const PREVIEWABLE_HINTS = new Set(['image', 'video', 'audio', 'text', 'code']);

function buildPreviewList() {
  return state.entries.filter(e => !e.is_dir && PREVIEWABLE_HINTS.has(e.mime_hint));
}

function updatePreviewNav() {
  const prevBtn = document.getElementById('preview-prev');
  const nextBtn = document.getElementById('preview-next');
  const hasMultiple = state.previewList.length > 1;
  prevBtn.style.display = hasMultiple ? '' : 'none';
  nextBtn.style.display = hasMultiple ? '' : 'none';
  prevBtn.disabled = state.previewIndex <= 0;
  nextBtn.disabled = state.previewIndex >= state.previewList.length - 1;
}

function navigatePreview(dir) {
  const newIdx = state.previewIndex + dir;
  if (newIdx < 0 || newIdx >= state.previewList.length) return;
  openPreview(state.previewList[newIdx]);
}

async function openPreview(entry) {
  const list = buildPreviewList();
  const idx = list.findIndex(e => e.path === entry.path);
  state.previewList = list;
  state.previewIndex = idx;

  const container = document.getElementById('preview-container');
  const title = document.getElementById('preview-title');
  const dlBtn = document.getElementById('preview-download');
  const moveBtn = document.getElementById('preview-move');
  const copyBtn = document.getElementById('preview-copy');
  const deleteBtn = document.getElementById('preview-delete');

  title.textContent = entry.name;
  dlBtn.href = '/api/download?path=' + encodeURIComponent(entry.path);
  dlBtn.download = entry.name;

  moveBtn.onclick = () => { closeModal('modal-preview'); openMoveModal(entry.path); };
  copyBtn.onclick = () => { closeModal('modal-preview'); openCopyModal(entry.path); };
  deleteBtn.onclick = () => { closeModal('modal-preview'); deleteFiles([entry.path]); };

  document.getElementById('preview-prev').onclick = () => navigatePreview(-1);
  document.getElementById('preview-next').onclick = () => navigatePreview(1);
  updatePreviewNav();

  container.innerHTML = '';

  const hint = entry.mime_hint;

  if (hint === 'image') {
    const img = document.createElement('img');
    img.src = '/api/download?path=' + encodeURIComponent(entry.path);
    img.alt = entry.name;
    container.appendChild(img);
    openModal('modal-preview');
  } else if (hint === 'video') {
    const vid = document.createElement('video');
    vid.controls = true;
    vid.autoplay = false;
    vid.style.maxWidth = '100%';
    const src = document.createElement('source');
    src.src = '/api/download?path=' + encodeURIComponent(entry.path);
    vid.appendChild(src);
    container.appendChild(vid);
    openModal('modal-preview');
  } else if (hint === 'audio') {
    const aud = document.createElement('audio');
    aud.controls = true;
    aud.style.width = '100%';
    const src = document.createElement('source');
    src.src = '/api/download?path=' + encodeURIComponent(entry.path);
    aud.appendChild(src);
    container.appendChild(aud);
    openModal('modal-preview');
  } else if (hint === 'text' || hint === 'code') {
    try {
      const data = await apiGet('/api/read?path=' + encodeURIComponent(entry.path));
      const pre = document.createElement('pre');
      pre.textContent = data.content;
      container.appendChild(pre);
      openModal('modal-preview');
    } catch (e) {
      toast('Cannot preview: ' + e.message, 'error');
    }
  } else {
    // For other file types, just trigger download
    downloadFile(entry.path);
  }
}

// ─── Disk info ────────────────────────────────────────────────────────────────
async function loadDiskInfo() {
  try {
    const params = new URLSearchParams({ path: state.currentPath });
    const data = await apiGet('/api/info?' + params);
    const disk = data.disk;
    if (!disk) return;
    const pct = Math.round(disk.used_pct);
    document.getElementById('disk-pct').textContent = pct + '%';
    document.getElementById('disk-used').textContent = formatSize(disk.used);
    document.getElementById('disk-total').textContent = formatSize(disk.total);
    const fill = document.getElementById('disk-bar-fill');
    fill.style.width = pct + '%';
    fill.className = 'disk-bar-fill' + (pct >= 90 ? ' danger' : pct >= 75 ? ' warn' : '');
  } catch (_) { /* non-critical */ }
}

// ─── Favourites ───────────────────────────────────────────────────────────────
async function loadFavourites() {
  try {
    const favs = await apiGet('/api/favourites');
    state.favourites = favs || [];
    const list = document.getElementById('fav-list');
    list.innerHTML = '';
    state.favourites.forEach(fav => {
      const el = document.createElement('div');
      el.className = 'fav-item';
      el.dataset.path = fav.path;

      // Icon (direct child of fav-item for proper gap spacing)
      const iconEl = document.createElement('span');
      iconEl.innerHTML = ICONS.dir; // safe: ICONS.dir is a static constant

      // Name only (without icon)
      const nameSpan = document.createElement('span');
      nameSpan.className = 'fav-name';
      nameSpan.textContent = fav.name;

      // Remove button (hidden until hover)
      const removeBtn = document.createElement('button');
      removeBtn.className = 'fav-remove-btn';
      removeBtn.title = 'Remove from Favourites';
      removeBtn.textContent = '✕';
      removeBtn.onclick = async (e) => {
        e.stopPropagation();
        try {
          await apiPost('/api/favourites/remove', { path: fav.path });
          await loadFavourites();
          renderFileList();
        } catch (err) {
          toast(err.message, 'error');
        }
      };

      el.appendChild(iconEl);
      el.appendChild(nameSpan);
      el.appendChild(removeBtn);
      el.onclick = () => loadDirectory(fav.path);
      list.appendChild(el);
    });
    updateActiveFav();
  } catch (_) { /* non-critical */ }
}

function isFavourite(path) {
  return state.favourites.some(f => f.path === path);
}

async function toggleFavourite(path, name, btn) {
  try {
    if (isFavourite(path)) {
      await apiPost('/api/favourites/remove', { path });
    } else {
      await apiPost('/api/favourites/add', { path, name });
    }
    await loadFavourites();
    // Update just this button to reflect the new state
    if (btn) {
      const isNowFav = isFavourite(path);
      btn.innerHTML = isNowFav ? ICONS.starFilled : ICONS.starEmpty;
      btn.classList.toggle('fav-active', isNowFav);
      btn.title = isNowFav ? 'Remove from Favourites' : 'Add to Favourites';
    }
  } catch (err) {
    toast(err.message, 'error');
  }
}

function updateActiveFav() {
  document.querySelectorAll('.fav-item').forEach(el => {
    el.classList.toggle('active', el.dataset.path === state.currentPath);
  });
}
function openModal(id) {
  const el = document.getElementById(id);
  if (el) { el.classList.add('open'); }
}

function closeModal(id) {
  const el = document.getElementById(id);
  if (el) {
    el.classList.remove('open');
    // Stop any media playing inside the modal
    el.querySelectorAll('video, audio').forEach(m => { m.pause(); m.src = ''; });
  }
}

// ─── Drag-and-drop ────────────────────────────────────────────────────────────
function initDragDrop() {
  const overlay = document.getElementById('drop-overlay');
  let dragCounter = 0;

  document.addEventListener('dragenter', e => {
    if (!e.dataTransfer.types.includes('Files')) return;
    dragCounter++;
    overlay.classList.add('visible');
  });
  document.addEventListener('dragleave', e => {
    dragCounter--;
    if (dragCounter <= 0) { dragCounter = 0; overlay.classList.remove('visible'); }
  });
  document.addEventListener('dragover', e => { e.preventDefault(); });
  document.addEventListener('drop', e => {
    e.preventDefault();
    dragCounter = 0;
    overlay.classList.remove('visible');
    if (e.dataTransfer.files.length > 0) uploadFiles(Array.from(e.dataTransfer.files));
  });
}

// ─── Font size ────────────────────────────────────────────────────────────────
const FONT_SIZE_KEY = 'filex_font_size';
const FONT_SIZE_MIN = 11;
const FONT_SIZE_MAX = 20;
const FONT_SIZE_DEFAULT = 15;

function loadFontSize() {
  const saved = parseInt(localStorage.getItem(FONT_SIZE_KEY), 10);
  const size = (saved >= FONT_SIZE_MIN && saved <= FONT_SIZE_MAX) ? saved : FONT_SIZE_DEFAULT;
  applyFontSize(size);
}

function applyFontSize(size) {
  document.documentElement.style.setProperty('--font-size-base', size + 'px');
  const label = document.getElementById('font-size-label');
  if (label) label.textContent = size + 'px';
  localStorage.setItem(FONT_SIZE_KEY, size);
}

function changeFontSize(delta) {
  const current = parseInt(getComputedStyle(document.documentElement).getPropertyValue('--font-size-base'), 10) || FONT_SIZE_DEFAULT;
  const next = Math.min(FONT_SIZE_MAX, Math.max(FONT_SIZE_MIN, current + delta));
  applyFontSize(next);
}

// ─── Keyboard shortcuts ───────────────────────────────────────────────────────
document.addEventListener('keydown', e => {
  // Ignore if inside input/textarea
  if (e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA') return;

  // Arrow navigation inside preview modal
  if (document.getElementById('modal-preview').classList.contains('open')) {
    if (e.key === 'ArrowLeft')  { e.preventDefault(); navigatePreview(-1); return; }
    if (e.key === 'ArrowRight') { e.preventDefault(); navigatePreview(1);  return; }
  }

  if (e.key === 'F5') { e.preventDefault(); loadDirectory(state.currentPath); }
  if (e.key === 'Delete') {
    if (state.selectedFiles.size > 0) deleteFiles([...state.selectedFiles]);
  }
  if (e.key === 'Escape') {
    document.querySelectorAll('.modal-backdrop.open').forEach(m => closeModal(m.id));
  }
});

// ─── Init ─────────────────────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', () => {
  // Apply saved font size immediately
  loadFontSize();

  // Read initial path from URL
  const url = new URL(window.location);
  const initPath = url.searchParams.get('path') || '/';

  // Check auth and get current user
  fetch('/api/me').then(r => {
    if (r.status === 401) { window.location.href = '/login'; return null; }
    return r.json();
  }).then(me => {
    if (!me) return;
    if (me.auth_required && me.username) {
      const info = document.getElementById('user-info');
      if (info) {
        info.style.display = 'flex';
        document.getElementById('username-display').textContent = me.username;
      }
    }
  }).catch(() => {});

  // Load config to get show_dotfiles default
  fetch('/api/config').then(r => r.json()).then(cfg => {
    if (cfg && typeof cfg.show_dotfiles === 'boolean') {
      state.showDotfiles = cfg.show_dotfiles;
      updateDotfilesBtn();
    }
  }).catch(() => {}).finally(() => {
    loadFavourites();
    loadDirectory(initPath);
  });

  loadVersion();

  // Upload button
  document.getElementById('btn-upload').addEventListener('click', () => {
    document.getElementById('file-input').click();
  });

  // Logout button
  const btnLogout = document.getElementById('btn-logout');
  if (btnLogout) {
    btnLogout.addEventListener('click', async () => {
      try {
        await fetch('/api/logout', { method: 'POST' });
      } catch (_) {
        // Ignore network errors — clear local state and redirect anyway
      }
      window.location.href = '/login';
    });
  }
  document.getElementById('file-input').addEventListener('change', e => {
    uploadFiles(Array.from(e.target.files));
    e.target.value = '';
  });

  // Mkdir button
  document.getElementById('btn-mkdir').addEventListener('click', openMkdirModal);
  document.getElementById('mkdir-confirm').addEventListener('click', doMkdir);
  document.getElementById('mkdir-name').addEventListener('keydown', e => {
    if (e.key === 'Enter') doMkdir();
  });

  // Dotfiles toggle
  document.getElementById('btn-dotfiles').addEventListener('click', () => {
    state.showDotfiles = !state.showDotfiles;
    updateDotfilesBtn();
    loadDirectory(state.currentPath);
  });

  // Delete selected
  document.getElementById('btn-delete-sel').addEventListener('click', () => {
    deleteFiles([...state.selectedFiles]);
  });

  // Move selected
  document.getElementById('btn-move-sel').addEventListener('click', () => {
    openMoveModal([...state.selectedFiles]);
  });

  // Copy selected
  document.getElementById('btn-copy-sel').addEventListener('click', () => {
    openCopyModal([...state.selectedFiles]);
  });

  // Refresh
  document.getElementById('btn-refresh').addEventListener('click', () => {
    loadDirectory(state.currentPath);
  });

  // Font size controls
  document.getElementById('btn-font-dec').addEventListener('click', () => changeFontSize(-1));
  document.getElementById('btn-font-inc').addEventListener('click', () => changeFontSize(+1));

  // Select-all checkbox
  document.getElementById('select-all').addEventListener('change', e => {
    const checked = e.target.checked;
    state.entries.forEach(entry => {
      if (checked) state.selectedFiles.add(entry.path);
      else state.selectedFiles.delete(entry.path);
    });
    renderFileList();
    updateSelectionButtons();
  });

  // Sort headers
  document.querySelectorAll('.file-table th[data-sort]').forEach(th => {
    th.addEventListener('click', () => setSort(th.dataset.sort));
  });

  // Editor save
  document.getElementById('editor-save').addEventListener('click', saveEditor);

  // Copy confirm
  document.getElementById('copy-confirm').addEventListener('click', doCopy);
  document.getElementById('copy-dst').addEventListener('keydown', e => {
    if (e.key === 'Enter') doCopy();
  });
  document.getElementById('copy-browser-up').addEventListener('click', () => folderBrowserUp('copy'));

  // Download selected
  document.getElementById('btn-download-sel').addEventListener('click', () => {
    downloadZip([...state.selectedFiles]);
  });

  // Move confirm
  document.getElementById('move-confirm').addEventListener('click', doMove);
  document.getElementById('move-dst').addEventListener('keydown', e => {
    if (e.key === 'Enter') doMove();
  });
  document.getElementById('move-browser-up').addEventListener('click', () => folderBrowserUp('move'));

  // Modal close buttons (data-close attribute)
  document.querySelectorAll('[data-close]').forEach(btn => {
    btn.addEventListener('click', () => closeModal(btn.dataset.close));
  });

  // Close modal on backdrop click
  document.querySelectorAll('.modal-backdrop').forEach(backdrop => {
    backdrop.addEventListener('click', e => {
      if (e.target === backdrop) closeModal(backdrop.id);
    });
  });

  // Mobile sidebar toggle
  document.getElementById('menu-toggle').addEventListener('click', () => {
    document.getElementById('sidebar').classList.toggle('open');
    document.getElementById('sidebar-overlay').classList.toggle('visible');
  });
  document.getElementById('sidebar-overlay').addEventListener('click', () => {
    document.getElementById('sidebar').classList.remove('open');
    document.getElementById('sidebar-overlay').classList.remove('visible');
  });

  // Browser back/forward
  window.addEventListener('popstate', e => {
    const path = (e.state && e.state.path) || '/';
    loadDirectory(path);
  });

  initDragDrop();

  // Cancel active operation
  document.getElementById('op-progress-cancel').addEventListener('click', () => {
    if (state.opAbort) state.opAbort();
  });
});

function updateDotfilesBtn() {
  document.getElementById('btn-dotfiles').classList.toggle('active', state.showDotfiles);
}

async function loadVersion() {
  try {
    const data = await apiGet('/api/version');
    const el = document.getElementById('sidebar-version');
    if (el && data && data.version) {
      el.textContent = 'v' + data.version;
    }
  } catch (_) { /* non-critical */ }
}

// ─── Folder info popup ────────────────────────────────────────────────────────
let _folderInfoPopup = null;
let _folderInfoListeners = null;

function closeFolderInfoPopup() {
  if (_folderInfoPopup) {
    _folderInfoPopup.remove();
    _folderInfoPopup = null;
  }
  if (_folderInfoListeners) {
    document.removeEventListener('mousedown', _folderInfoListeners.onOutside);
    document.removeEventListener('keydown', _folderInfoListeners.onKey);
    _folderInfoListeners = null;
  }
}

function showFolderInfoPopup(entry, anchorBtn) {
  closeFolderInfoPopup();

  const popup = document.createElement('div');
  popup.className = 'folder-info-popup';
  _folderInfoPopup = popup;

  const title = document.createElement('div');
  title.className = 'folder-info-popup-title';
  title.textContent = entry.name;
  popup.appendChild(title);

  function makeRow(label, value) {
    const row = document.createElement('div');
    row.className = 'folder-info-popup-row';
    const lbl = document.createElement('span');
    lbl.className = 'folder-info-popup-label';
    lbl.textContent = label;
    const val = document.createElement('span');
    val.className = 'folder-info-popup-value';
    val.textContent = value;
    row.appendChild(lbl);
    row.appendChild(val);
    return { row, val };
  }

  const { row: pathRow } = makeRow('Path', entry.path);
  const { row: dateRow } = makeRow('Modified', formatDate(entry.mod_time));
  const { row: sizeRow, val: sizeVal } = makeRow('Total size', '…');
  const { row: countRow, val: countVal } = makeRow('Files', '…');

  popup.appendChild(pathRow);
  popup.appendChild(dateRow);
  popup.appendChild(sizeRow);
  popup.appendChild(countRow);

  document.body.appendChild(popup);

  // Position popup near the anchor button
  const rect = anchorBtn.getBoundingClientRect();
  const popupW = popup.offsetWidth || 240;
  const popupH = popup.offsetHeight || 160;
  let top = rect.bottom + 6;
  let left = rect.left;
  if (left + popupW > window.innerWidth - 8) left = window.innerWidth - popupW - 8;
  if (top + popupH > window.innerHeight - 8) top = rect.top - popupH - 6;
  popup.style.top = top + 'px';
  popup.style.left = left + 'px';

  // Fetch folder info from backend
  const params = new URLSearchParams({ path: entry.path });
  apiGet('/api/folder-info?' + params).then(data => {
    sizeVal.textContent = formatSize(data.total_size);
    countVal.textContent = data.file_count.toLocaleString() + (data.file_count === 1 ? ' file' : ' files');
  }).catch(() => {
    sizeVal.textContent = '—';
    countVal.textContent = '—';
  });

  // Close on outside click or Escape; store refs so closeFolderInfoPopup() can remove them
  const onOutside = (e) => {
    if (!popup.contains(e.target) && e.target !== anchorBtn) {
      closeFolderInfoPopup();
    }
  };
  const onKey = (e) => {
    if (e.key === 'Escape') {
      closeFolderInfoPopup();
    }
  };
  _folderInfoListeners = { onOutside, onKey };
  setTimeout(() => {
    document.addEventListener('mousedown', onOutside);
    document.addEventListener('keydown', onKey);
  }, 0);
}
