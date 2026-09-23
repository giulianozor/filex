'use strict';

// ─── State ────────────────────────────────────────────────────────────────────
const state = {
  currentPath: '/',
  showDotfiles: false,
  sortBy: 'name',
  sortDir: 'asc',
  filterText: '',
  selectedFiles: new Set(),
  entries: [],
  moveSrc: null,
  copySrc: null,
  deletePaths: null,
  moveInProgress: false,
  copyInProgress: false,
  editorPath: null,
  favourites: [],
  protectedPaths: [],
  opAbort: null,   // function to cancel the current running operation
  uploadTasks: [],
  uploadTaskSeq: 0,
  uploadRefreshTimer: null,
  previewList: [],  // previewable file entries in the current directory
  previewIndex: -1, // index of the currently previewed entry in previewList
  slideshowActive: false,
  slideshowImages: [],
  slideshowIndex: 0,
  slideshowInterval: 1000,
  slideshowTimer: null,
  slideshowPaused: false,
  slideshowResumeAfterZoom: false,
  slideshowControlsTimer: null,
  slideshowZoom: 1,
  wipeMethods: [],
  ffmpegAvailable: false,
  unzipAvailable: false,
  sevenzipAvailable: false,
  unrarAvailable: false,
  tarAvailable: false,
  videoEditorEntry: null,
  unsupportedEntry: null,
  videoEditorIntervals: [],
  batchThumbCancel: null,
  videoEditorStartTime: null,
  videoEditorEndTime: null,
};

// Resolve function for the pending delete confirmation modal promise
let _deleteModalResolve = null;

// Resolve function for the pending navigation confirmation modal promise
let _navigateModalResolve = null;

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
  lockClosed: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>`,
  lockOpen:   `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 9.9-1"/></svg>`,
  lockInherited: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>`,
  download: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>`,
  extract: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/><line x1="12" y1="17" x2="12" y2="11"/><polyline points="9 14 12 11 15 14"/></svg>`,
  edit: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>`,
  info: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="8" x2="12" y2="12"/><line x1="12" y1="16" x2="12.01" y2="16"/></svg>`,
  rename: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="12" y1="5" x2="12" y2="19"/><path d="M8 5h8"/><path d="M8 19h8"/></svg>`,
  trash: `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/></svg>`,
};

function getIcon(entry) {
  return ICONS[entry.mime_hint] || (entry.mime_hint === 'markdown' ? ICONS.text : ICONS.file);
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

// safeMarkdownUrl escapes a URL attribute value and blocks schemes that would
// execute script when clicked or loaded (mitigating XSS via crafted markdown).
// escapeAttr already escapes quotes, so using it here is sufficient.
function safeMarkdownUrl(url) {
  const trimmed = (url || '').trim();
  const lower = trimmed.toLowerCase().replace(/[\u0000-\u0020\u007f]/g, '');
  if (/^(javascript|vbscript|data):/.test(lower)) {
    return '#';
  }
  return escapeAttr(trimmed);
}

function basename(p) {
  return p.replace(/\/$/, '').split('/').pop() || '/';
}

function parentPath(p) {
  return p.split('/').slice(0, -1).join('/') || '/';
}

function setTopProgress(running) {
  const bar = document.getElementById('upload-progress-bar');
  if (running) {
    bar.classList.add('indeterminate');
  } else {
    bar.classList.remove('indeterminate');
    bar.style.display = 'none';
  }
}

function isDestinationConflictError(err) {
  if (err?.code === 'destination_exists') return true;
  return typeof err?.message === 'string' && err.message.toLowerCase().includes('destination already exists');
}

function makeAbortError(msg = 'Operation cancelled') {
  const e = new Error(msg);
  e.name = 'AbortError';
  return e;
}

// bindEnterToInput runs fn when Enter is pressed inside inputId (creating the
// folder/file from the modal form fields).
function bindEnterToInput(inputId, fn) {
  document.getElementById(inputId).addEventListener('keydown', e => {
    if (e.key === 'Enter') fn();
  });
}

// bindDstEnter is the delegated listener shared by the move/copy modal keydown
// events: it triggers the transfer when the focus is on the destination input.
function bindDstEnter(e, inputId, fn) {
  if (e.target.id === inputId && e.key === 'Enter') fn();
}

// extensionOf returns the lowercased single-suffix (".txt", ".gz", ...) of a
// basename, or '' for dotfiles/extensionless names — mirrors the extension
// selection dialog's long-standing grouping behavior.
function extensionOf(name) {
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(dot).toLowerCase() : '';
}

function archiveStem(name) {
  const lower = name.toLowerCase();
  for (const double of ['.tar.gz', '.tar.bz2', '.tar.xz', '.tgz', '.tbz2']) {
    if (lower.endsWith(double)) return name.slice(0, -double.length);
  }
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(0, dot) : name;
}

function askConflictResolution(actionLabel, src, dst) {
  return new Promise((resolve) => {
    const item = basename(src);
    const msg = document.getElementById('conflict-message');
    msg.innerHTML = ''; // clear
    const parts = [
      document.createTextNode(`${actionLabel} conflict: "`),
      (() => { const s = document.createElement('strong'); s.textContent = item; return s; })(),
      document.createTextNode('" already exists in "'),
      (() => { const s = document.createElement('strong'); s.textContent = dst; return s; })(),
      document.createTextNode('".'),
    ];
    parts.forEach(el => msg.appendChild(el));
    const br = document.createElement('br');
    msg.appendChild(br);
    msg.appendChild(document.createTextNode('Choose how to proceed:'));

    const modal = document.getElementById('modal-conflict');
    const applyCheck = document.getElementById('conflict-apply-all');
    applyCheck.checked = false;

    const cancelBtn = document.getElementById('conflict-cancel');
    const overwriteBtn = document.getElementById('conflict-overwrite');
    const renameBtn = document.getElementById('conflict-rename');
    const closeBtn = document.getElementById('conflict-close');

    const cleanup = () => {
      cancelBtn.removeEventListener('click', onCancel);
      overwriteBtn.removeEventListener('click', onOverwrite);
      renameBtn.removeEventListener('click', onRename);
      closeBtn.removeEventListener('click', onCancel);
      modal.removeEventListener('click', onBackdropClick);
    };

    // done is the single resolver for this dialog. It is also stored at module
    // scope (_conflictResolve) so that closing the modal by any other path —
    // Escape, the ✕ button, a data-close handler — resolves the pending
    // promise as a cancel instead of leaving the move/copy awaiting forever.
    const done = (v) => {
      if (_conflictResolve === done) _conflictResolve = null;
      cleanup();
      resolve(v);
    };

    const onCancel = () => {
      done({ action: 'cancel', applyToAll: false });
      closeModal('modal-conflict', false);
    };
    const onOverwrite = () => {
      done({ action: 'overwrite', applyToAll: applyCheck.checked });
      closeModal('modal-conflict', false);
    };
    const onRename = () => {
      done({ action: 'rename', applyToAll: applyCheck.checked });
      closeModal('modal-conflict', false);
    };
    const onBackdropClick = (e) => {
      if (e.target === modal) onCancel();
    };

    cancelBtn.addEventListener('click', onCancel);
    overwriteBtn.addEventListener('click', onOverwrite);
    renameBtn.addEventListener('click', onRename);
    closeBtn.addEventListener('click', onCancel);
    modal.addEventListener('click', onBackdropClick);

    _conflictResolve = done;
    openModal('modal-conflict');
  });
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

function toastError(err, what) {
  toast((what ? what + ': ' : '') + (err && err.message ? err.message : String(err)), 'error');
}

// ─── API helpers ──────────────────────────────────────────────────────────────
function errorFromData(data, statusText) {
  const err = new Error((data && data.error) || statusText);
  if (data && typeof data.code === 'string') err.code = data.code;
  if (data && Array.isArray(data.conflicts)) err.conflicts = data.conflicts;
  return err;
}

// Unified auth handling: returns true (and redirects) when the session expired.
function isUnauthorized(resp) {
  if (resp.status !== 401) return false;
  window.location.href = '/login';
  return true;
}

async function apiGet(url) {
  const r = await fetch(url);
  if (isUnauthorized(r)) return null;
  const data = await r.json();
  if (!r.ok) throw errorFromData(data, r.statusText);
  return data;
}

async function apiPost(url, body, opts = {}) {
  const r = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal: opts.signal,
  });
  if (isUnauthorized(r)) return null;
  const data = await r.json();
  if (!r.ok) throw errorFromData(data, r.statusText);
  return data;
}

// apiList fetches the directory listing for path, honouring the dotfiles toggle.
function apiList(path, dotfiles) {
  return apiGet('/api/list?' + new URLSearchParams({ path, dotfiles }));
}

// ─── Directory loading ────────────────────────────────────────────────────────
// _loadSeq fences concurrent loadDirectory calls: a slow response for an
// earlier navigation must not overwrite the listing of a newer one that
// happened to resolve first (navigation race on fast serial clicks).
let _loadSeq = 0;

// _filterTimer is the debounce handle for the filter input; it lives at module
// scope so loadDirectory can cancel a pending re-filter when it navigates away.
let _filterTimer = null;

// _previewSeq fences concurrent openPreview() calls: a slow text/markdown read
// must not append into the container of a newer preview opened in between.
let _previewSeq = 0;

// _editorSeq fences concurrent openEditor() calls the same way: a slow read
// must not clobber the textarea of a file opened afterwards.
let _editorSeq = 0;

async function loadDirectory(path, { addHistory = true } = {}) {
  if (state.selectedFiles.size > 0) {
    const proceed = await new Promise(resolve => {
      document.getElementById('navigate-confirm-msg').textContent =
        `${state.selectedFiles.size} item(s) selected. Discard selection and navigate?`;
      _navigateModalResolve = resolve;
      openModal('modal-navigate');
    });
    if (!proceed) return;
  }
  path = path || '/';
  state.currentPath = path;
  state.filterText = '';
  const filterInput = document.getElementById('filter-input');
  if (filterInput) filterInput.value = '';
  document.getElementById('filter-clear').classList.remove('visible');
  state.selectedFiles.clear();
  updateSelectionButtons();

  const seq = ++_loadSeq;
  // Cancel a pending re-filter: navigating replaces the listing, so a queued
  // 300ms filter step for the old directory must not fire into the new one.
  if (_filterTimer) {
    clearTimeout(_filterTimer);
    _filterTimer = null;
  }
  try {
    const entries = await apiList(path, state.showDotfiles);
    if (seq !== _loadSeq) return; // superseded by a newer navigation
    state.entries = entries || [];
    await loadProtectedPaths();
    renderBreadcrumb();
    renderFileList();
    loadDiskInfo();
    if (addHistory) updateUrl(path);
  } catch (e) {
    toastError(e, 'Error loading directory');
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

function getFilteredEntries() {
  const sorted = getSortedEntries();
  if (!state.filterText) return sorted;
  const lower = state.filterText.toLowerCase();
  return sorted.filter(e => e.name.toLowerCase().includes(lower));
}

function escapeText(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// escapeAttr is escapeText plus attribute-context quote escaping.
function escapeAttr(s) {
  return escapeText(s).replace(/"/g, '&quot;');
}

// Map from entry path → entry for O(1) lookup by event delegation
const _entryByPath = new Map();

function renderFileList() {
  const tbody = document.getElementById('file-body');
  const empty = document.getElementById('empty-state');
  const entries = getFilteredEntries();

  _entryByPath.clear();
  for (let i = 0; i < entries.length; i++) _entryByPath.set(entries[i].path, entries[i]);

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

  if (entries.length === 0) {
    tbody.innerHTML = '';
    empty.style.display = 'block';
    empty.querySelector('p').textContent = state.filterText ? 'No files match your filter.' : 'This folder is empty';
    updateSelectAllState();
    document.getElementById('btn-slideshow').style.display = 'none';
    return;
  }
  empty.style.display = 'none';

  const DL_SVG = ICONS.download;
  const EXTRACT_SVG = ICONS.extract;
  const EDIT_SVG = ICONS.edit;
  const INFO_SVG = ICONS.info;
  const RENAME_SVG = ICONS.rename;

  const parts = new Array(entries.length);
  for (let i = 0; i < entries.length; i++) {
    const e = entries[i];
    const sel = state.selectedFiles.has(e.path);
    const dp = escapeAttr(e.path);
    const en = escapeText(e.name);

    let actions = '';

    if (!e.is_dir) {
      actions += `<button class="icon-btn" title="Download" data-action="download" data-path="${dp}">${DL_SVG}</button>`;
      if (e.mime_hint === 'archive' && canExtractArchive(e)) {
        actions += `<button class="icon-btn" title="Extract archive" data-action="extract" data-path="${dp}">${EXTRACT_SVG}</button>`;
      }
      if (['text','code','file'].includes(e.mime_hint)) {
        actions += `<button class="icon-btn" title="Edit" data-action="edit" data-path="${dp}">${EDIT_SVG}</button>`;
      }
    }

    if (e.is_dir) {
      actions += `<button class="icon-btn" title="Folder info" data-action="folder-info" data-path="${dp}">${INFO_SVG}</button>`;
      const fav = isFavourite(e.path);
      actions += `<button class="icon-btn${fav ? ' fav-active' : ''}" title="${fav ? 'Remove from Favourites' : 'Add to Favourites'}" data-action="favourite" data-path="${dp}">${fav ? ICONS.starFilled : ICONS.starEmpty}</button>`;
      actions += `<button class="icon-btn" title="Download as zip" data-action="download-zip" data-path="${dp}">${ICONS.download}</button>`;
    }

    actions += `<button class="icon-btn" title="Rename" data-action="rename" data-path="${dp}">${RENAME_SVG}</button>`;

    actions += protectButtonHtml(e.path);

    parts[i] = `<tr data-path="${dp}" class="${sel ? 'selected' : ''}"><td><input type="checkbox" class="row-check"${sel ? ' checked' : ''}></td><td><div class="file-name-cell">${getIcon(e)}<span class="file-name${e.is_dir ? ' is-dir' : ''}">${en}</span></div></td><td class="file-size">${e.is_dir ? '—' : formatSize(e.size)}</td><td class="file-date">${formatDate(e.mod_time)}</td><td><div class="actions">${actions}</div></td></tr>`;
  }

  tbody.innerHTML = parts.join('');
  updateSelectAllState();

  // Show slideshow button only if images are present
  const hasImages = entries.some(e => !e.is_dir && e.mime_hint === 'image');
  document.getElementById('btn-slideshow').style.display = hasImages ? '' : 'none';
}

// ─── Event delegation for file list (replaces per-row listeners) ──────────────
{
  let _longPressTimer = null;
  let _longPressTr = null;
  let _longPressStartX = 0, _longPressStartY = 0;

  const lpCancel = () => {
    if (_longPressTimer) { clearTimeout(_longPressTimer); _longPressTimer = null; }
    if (_longPressTr) { _longPressTr.classList.remove('long-press-active'); _longPressTr = null; }
  };

  document.addEventListener('touchstart', e => {
    const tr = e.target.closest('#file-body > tr');
    if (!tr || e.target.closest('button, input, a')) return;
    const t = e.touches[0];
    _longPressStartX = t.clientX; _longPressStartY = t.clientY;
    _longPressTr = tr;
    tr.classList.add('long-press-active');
    _longPressTimer = setTimeout(() => {
      _longPressTimer = null; _longPressTr = null;
      tr.classList.remove('long-press-active');
      const path = tr.dataset.path;
      const entry = _entryByPath.get(path);
      if (!entry) return;
      const nowSelected = !state.selectedFiles.has(path);
      const cb = tr.querySelector('.row-check');
      if (cb) cb.checked = nowSelected;
      toggleSelect(path, nowSelected, tr);
      if (navigator.vibrate) navigator.vibrate(30);
    }, 500);
  }, { passive: true });

  document.addEventListener('touchmove', e => {
    if (_longPressTimer) {
      const t = e.touches[0];
      if (Math.abs(t.clientX - _longPressStartX) > 8 || Math.abs(t.clientY - _longPressStartY) > 8) {
        lpCancel();
      }
    }
  }, { passive: true });

  document.addEventListener('touchend', lpCancel, { passive: true });
  document.addEventListener('touchcancel', lpCancel, { passive: true });

  // Click delegation for the file list
  document.getElementById('file-body').addEventListener('click', e => {
    // Checkbox
    const cb = e.target.closest('.row-check');
    if (cb) {
      const tr = cb.closest('tr');
      if (tr) toggleSelect(tr.dataset.path, cb.checked, tr);
      return;
    }

    // Name click → navigate dir or preview file
    const nameEl = e.target.closest('.file-name');
    if (nameEl) {
      const tr = nameEl.closest('tr');
      if (!tr) return;
      const entry = _entryByPath.get(tr.dataset.path);
      if (!entry) return;
      if (entry.is_dir) loadDirectory(entry.path);
      else openPreview(entry);
      return;
    }

    // Action buttons
    const btn = e.target.closest('.icon-btn[data-action]');
    if (!btn) return;
    const action = btn.dataset.action;
    const path = btn.dataset.path;
    const entry = _entryByPath.get(path);
    if (!entry) return;

    switch (action) {
      case 'download':     downloadFile(path); break;
      case 'extract':      openArchiveExtract(entry); break;
      case 'edit':         openEditor(entry); break;
      case 'folder-info':  showFolderInfoPopup(entry, btn); break;
      case 'favourite':    toggleFavourite(path, entry.name, btn); break;
      case 'download-zip': downloadZip([path]); break;
      case 'rename':       startInlineRename(entry, btn.closest('tr').querySelector('.file-name')); break;
      case 'protect':      toggleProtection(entry, btn); break;
    }
  });
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
  const hasSelection = state.selectedFiles.size > 0;
  const hasProtected = hasSelection && [...state.selectedFiles].some(p => isProtected(p));
  const btn = document.getElementById('btn-delete-sel');
  if (btn) btn.style.display = hasSelection && !hasProtected ? '' : 'none';
  const dlBtn = document.getElementById('btn-download-sel');
  if (dlBtn) dlBtn.style.display = state.selectedFiles.size > 0 ? '' : 'none';
  const mvBtn = document.getElementById('btn-move-sel');
  if (mvBtn) mvBtn.style.display = state.selectedFiles.size > 0 ? '' : 'none';
  const cpBtn = document.getElementById('btn-copy-sel');
  if (cpBtn) cpBtn.style.display = state.selectedFiles.size > 0 ? '' : 'none';

  // Show Thumbnails button when 2+ video files selected and ffmpeg available
  const thumbBtn = document.getElementById('btn-thumbnails');
  if (thumbBtn) {
    if (hasSelection && state.ffmpegAvailable && state.entries.length > 0) {
      const selPaths = [...state.selectedFiles];
      const allVideos = selPaths.every(p => {
        const e = _entryByPath.get(p);
        return e && !e.is_dir && e.mime_hint === 'video';
      });
      thumbBtn.style.display = selPaths.length >= 2 && allVideos ? '' : 'none';
    } else {
      thumbBtn.style.display = 'none';
    }
  }
}

function updateSelectAllState() {
  const selectAll = document.getElementById('select-all');
  if (!selectAll) return;
  const targets = state.filterText ? getFilteredEntries() : state.entries;
  const count = targets.length;
  const sel = targets.filter(e => state.selectedFiles.has(e.path)).length;
  selectAll.checked = count > 0 && sel === count;
  selectAll.indeterminate = sel > 0 && sel < count;
}

// ─── Select by extension ─────────────────────────────────────────────────────
function openSelectExtModal() {
  const extSet = new Set();
  state.entries.forEach(e => {
    if (e.is_dir) return;
    const ext = extensionOf(e.name);
    if (ext) extSet.add(ext);
  });
  const exts = [...extSet].sort();
  const container = document.getElementById('ext-list');
  container.innerHTML = '';
  if (exts.length === 0) {
    container.innerHTML = '<div style="color:var(--text-dim);font-size:13px;padding:8px 0">No files with extensions.</div>';
    document.getElementById('ext-select-all-exts').style.display = 'none';
    document.getElementById('ext-deselect-all-exts').style.display = 'none';
  } else {
    document.getElementById('ext-select-all-exts').style.display = '';
    document.getElementById('ext-deselect-all-exts').style.display = '';
    exts.forEach(ext => {
      const label = document.createElement('label');
      label.style.cssText = 'display:flex;align-items:center;gap:8px;padding:3px 0;cursor:pointer;font-size:13px;font-family:monospace';
      const cb = document.createElement('input');
      cb.type = 'checkbox';
      cb.className = 'ext-cb';
      cb.value = ext;
      cb.checked = true;
      label.appendChild(cb);
      label.append(ext);
      container.appendChild(label);
    });
  }
  openModal('modal-select-ext');
}

function applyExtSelection(select) {
  const exts = new Set();
  document.querySelectorAll('.ext-cb:checked').forEach(cb => exts.add(cb.value));
  state.entries.forEach(e => {
    if (e.is_dir) return;
    const ext = extensionOf(e.name);
    if (ext && exts.has(ext)) {
      if (select) state.selectedFiles.add(e.path);
      else state.selectedFiles.delete(e.path);
    }
  });
  closeModal('modal-select-ext');
  renderFileList();
  updateSelectionButtons();
}

// ─── Sort ─────────────────────────────────────────────────────────────────────
function setSort(col) {
  if (state.sortBy === col) {
    state.sortDir = state.sortDir === 'asc' ? 'desc' : 'asc';
  } else {
    state.sortBy = col;
    state.sortDir = 'asc';
  }
  document.querySelectorAll('.file-table th[data-sort]').forEach(th => {
    th.setAttribute('aria-sort',
      th.dataset.sort === state.sortBy ? (state.sortDir === 'asc' ? 'ascending' : 'descending') : 'none');
  });
  renderFileList();
}

// ─── Inline rename ────────────────────────────────────────────────────────────
function startInlineRename(entry, nameSpan) {
  const oldName = entry.name;
  let escaped = false;
  const input = document.createElement('input');
  input.type = 'text';
  input.className = 'rename-input';
  input.value = oldName;
  nameSpan.replaceWith(input);
  input.focus();
  input.select();

  async function commit() {
    // Escape already restored the original name by replacing the input;
    // wait for the blur that follows and bail out instead of renaming.
    if (escaped) { input.replaceWith(nameSpan); return; }
    const newName = input.value.trim();
    if (!newName || newName === oldName) {
      input.replaceWith(nameSpan);
      return;
    }
    try {
      await apiPost('/api/rename', { path: entry.path, newname: newName });
      toast('Renamed to ' + newName, 'success');
      state.selectedFiles.clear();
      updateSelectionButtons();
      await loadProtectedPaths();
      loadDirectory(state.currentPath);
    } catch (e) {
      toastError(e, 'Rename failed');
      input.replaceWith(nameSpan);
    }
  }

  input.onblur = commit;
  input.onkeydown = e => {
    if (e.key === 'Enter') { e.preventDefault(); input.blur(); }
    if (e.key === 'Escape') {
      escaped = true;
      input.replaceWith(nameSpan);
    }
  };
}

// ─── Delete ───────────────────────────────────────────────────────────────────
function populateWipeMethodSelect() {
  const sel = document.getElementById('delete-wipe-method');
  if (!sel || state.wipeMethods.length === 0) return;
  const saved = (state.deletePrefs && state.deletePrefs.wipe_method) || 'fast';
  const current = sel.value;
  sel.innerHTML = '';
  state.wipeMethods.forEach(m => {
    const opt = document.createElement('option');
    opt.value = m.id;
    opt.textContent = `${m.name} — ${m.description}`;
    sel.appendChild(opt);
  });
  const pick = state.wipeMethods.some(m => m.id === saved) ? saved : 'fast';
  sel.value = current && state.wipeMethods.some(m => m.id === current) ? current : pick;
}

// applyDeletePrefs restores the user's remembered wipe toggle and method into
// the delete modal. The remembered values are stored server-side in
// ~/.config/filex.yaml and mirrored into state.deletePrefs, so whichever
// device opens the modal sees the same last choice.
function applyDeletePrefs() {
  const wipeCheck = document.getElementById('delete-wipe-check');
  if (wipeCheck) wipeCheck.checked = !!(state.deletePrefs && state.deletePrefs.default_wipe);
  const methodSelect = document.getElementById('delete-wipe-method');
  if (methodSelect) {
    const saved = (state.deletePrefs && state.deletePrefs.wipe_method) || 'fast';
    methodSelect.value = state.wipeMethods.some(m => m.id === saved) ? saved : 'fast';
  }
}

// loadDeletePrefs fetches the current user's remembered secure-delete settings
// so the wipe toggle/method survive reloads and across browsers.
async function loadDeletePrefs() {
  try {
    const r = await fetch('/api/delete-prefs');
    if (r.ok) state.deletePrefs = await r.json();
  } catch (_) {
    state.deletePrefs = null;
  }
  populateWipeMethodSelect();
}

// saveDeletePrefs persists the current remembered secure-delete settings to the
// server (which writes ~/.config/filex.yaml). Best-effort: a failed write only
// costs the cross-session memory, never the delete in progress.
async function saveDeletePrefs() {
  if (!state.deletePrefs) state.deletePrefs = {};
  try {
    await apiPost('/api/delete-prefs', state.deletePrefs);
  } catch (_) {}
}

// saveDotfiles persists the show-dotfiles toggle to the server (the per-user
// preference in ~/.config/filex.yaml, or the config.yaml default in no-auth
// mode). Best-effort: a failed write only costs the cross-session memory.
async function saveDotfiles() {
  try {
    await apiPost('/api/show-dotfiles', { show_dotfiles: state.showDotfiles });
  } catch (_) {}
}

async function deleteFiles(paths) {
  if (!paths || paths.length === 0) return false;
  // Re-entrancy guard: if a delete modal is already open (e.g. a double-click
  // on the delete-selected button), return the pending promise instead of
  // leaking a second one whose resolution would be lost.
  if (_deleteModalResolve) return _deleteModalResolve;
  const names = paths.map(basename);
  const isSingle = names.length === 1;

  // Count how many are directories vs files
  let dirCount = 0, fileCount = 0;
  paths.forEach(p => {
    const entry = state.entries.find(e => e.path === p);
    if (entry && entry.is_dir) dirCount++;
    else fileCount++;
  });
  const needsExtraConfirm = fileCount > 5 || dirCount > 1;

  // Populate the confirmation modal (avoids native confirm() which browsers can
  // suppress after bfcache page restoration / back-button navigation)
  document.getElementById('delete-confirm-msg').textContent =
    `Delete ${isSingle ? 'this item' : 'these ' + names.length + ' items'}?`;
  const ul = document.getElementById('delete-confirm-list');
  ul.innerHTML = '';
  names.forEach(name => {
    const li = document.createElement('li');
    li.textContent = name;
    ul.appendChild(li);
  });

  const extraWarn = document.getElementById('delete-extra-warning');
  const extraCheck = document.getElementById('delete-confirm-check');
  const deleteBtn = document.getElementById('delete-confirm');
  const extraMsg = document.getElementById('delete-extra-msg');
  if (extraWarn && extraCheck && deleteBtn && extraMsg) {
    if (needsExtraConfirm) {
      const parts = [];
      if (fileCount > 5) parts.push(`${fileCount} files`);
      if (dirCount > 1) parts.push(`${dirCount} folders`);
      extraMsg.textContent = `I understand that deleting ${parts.join(' and ')} cannot be undone`;
      extraWarn.style.display = '';
      extraCheck.checked = false;
      extraCheck.disabled = false;
      deleteBtn.disabled = true;
    } else {
      extraWarn.style.display = 'none';
      deleteBtn.disabled = false;
    }
  }

  // Wipe option panel: visible whenever the library-backed wipe is available
  // (it always is) so users can choose the secure-delete algorithm.
  const wipeOption = document.getElementById('delete-wipe-option');
  if (wipeOption) wipeOption.style.display = '';
  // Remember the last secure-delete choice, but never force it on: wiping
  // overwrites every byte and is O(file size × passes).
  applyDeletePrefs();

  state.deletePaths = paths;
  openModal('modal-delete');
  return new Promise(resolve => { _deleteModalResolve = resolve; });
}

async function doDelete() {
  const paths = state.deletePaths;
  const resolve = _deleteModalResolve;
  const wipe = document.getElementById('delete-wipe-check').checked;
  const wipeMethod = document.getElementById('delete-wipe-method').value || 'fast';
  state.deletePaths = null;
  _deleteModalResolve = null;
  closeModal('modal-delete', false);
  if (!paths || paths.length === 0) { if (resolve) resolve(false); return; }

  const controller = new AbortController();
  state.opAbort = () => controller.abort();
  const label = wipe ? 'Securely deleting\u2026' : 'Deleting\u2026';
  const sub = paths.length === 1 ? basename(paths[0]) : paths.length + ' items';

  try {
    if (!wipe) {
      // Plain delete is an instant metadata unlink regardless of file size.
      showOpProgress(label, null, sub);
      await apiPost('/api/delete', { paths, wipe: false }, { signal: controller.signal });
    } else {
      // Secure delete overwrites every byte (O(size)) so multi-GB files take
      // minutes; stream wipe's own percentage from the server instead of
      // leaving the progress stuck on "starting". Does not set a default:
      // secure delete only ever runs when explicitly checked in this dialog.
      showOpProgress(label, 0, formatTransferProgress(0, paths.length, basename(paths[0])));
      openOpProgressModal();
      const progress = (done, total, current, filePct) => {
        const pct = transferPercent(done, total, filePct);
        showOpProgress(label, pct, formatTransferProgress(done, total, current || basename(paths[0]), filePct));
      };
      const r = await fetch('/api/delete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ paths, wipe: true, method: wipeMethod }),
        signal: controller.signal,
      });
      const result = await readNdjsonStreamAndResult(r, (msg) => {
        if (msg.type === 'progress') {
          progress(msg.done, msg.total, msg.current, typeof msg.filePct === 'number' ? msg.filePct : null);
        }
      });
      if (result == null) {
        // Session expired: readNdjsonStreamAndResult already surfaced the
        // isUnauthorized redirect, so just settle the pending delete promise
        // without showing the success toast.
        if (resolve) resolve(false);
        return;
      }
    }
    toast(paths.length === 1
      ? (wipe ? `Securely deleted ${basename(paths[0])}` : `Deleted ${basename(paths[0])}`)
      : (wipe ? `Securely deleted ${paths.length} items` : `Deleted ${paths.length} items`), 'success');
    // Clear the selection *before* reloading so loadDirectory's "discard
    // selection and navigate?" guard does not pop a modal right after a
    // successful delete (stragglers from a multi-selection are dropped here,
    // matching the rename flow).
    state.selectedFiles.clear();
    updateSelectionButtons();
    loadDirectory(state.currentPath);
    if (resolve) resolve(true);
  } catch (e) {
    if (e.name === 'AbortError') {
      toast('Deletion cancelled', 'info');
      if (resolve) resolve(false);
    } else {
      toastError(e, 'Delete failed');
      if (resolve) resolve(false);
    }
  } finally {
    hideOpProgress();
  }
}

// ─── Download ─────────────────────────────────────────────────────────────────
function downloadFile(path) {
  triggerDownload(downloadUrl(path), basename(path));
}

// ─── Operation progress (sidebar) ────────────────────────────────────────────
function showOpProgress(label, pct, sub) {
  const el = document.getElementById('op-progress');
  if (!el) return;
  el.style.display = 'block';
  document.getElementById('op-progress-label').textContent = label;
  setProgressFill(document.getElementById('op-progress-fill'), pct);
  document.getElementById('op-progress-sub').textContent = sub || '';
  syncOpProgressModal(label, pct, sub);
}

// Toggles a progress fill between indeterminate (null pct) and a set percentage.
function setProgressFill(fill, pct) {
  if (pct == null) {
    fill.classList.add('indeterminate');
    fill.style.width = '';
  } else {
    fill.classList.remove('indeterminate');
    fill.style.width = pct + '%';
  }
}

function syncOpProgressModal(label, pct, sub) {
  const modal = document.getElementById('modal-op-progress');
  if (!modal || !modal.classList.contains('open')) return;
  document.getElementById('op-progress-modal-title').textContent = label;
  document.getElementById('op-progress-modal-status').textContent = sub || label;
  setProgressFill(document.getElementById('op-progress-modal-fill'), pct);
  document.getElementById('op-progress-modal-percent').textContent = pct == null ? '' : Math.round(pct) + '%';
}

function openOpProgressModal() {
  const label = document.getElementById('op-progress-label').textContent;
  const fill = document.getElementById('op-progress-fill');
  const pct = fill.classList.contains('indeterminate') ? null : parseFloat(fill.style.width);
  const sub = document.getElementById('op-progress-sub').textContent;
  syncOpProgressModal(label, pct, sub);
  openModal('modal-op-progress');
}

function hideOpProgress() {
  const el = document.getElementById('op-progress');
  if (el) el.style.display = 'none';
  state.opAbort = null;
  closeModal('modal-op-progress', false);
}

function formatTransferProgress(done, total, currentLabel, filePct) {
  if (!total || total <= 0) return '0%';
  const pct = Math.round((done / total) * 100);
  let s;
  if (total > 1) s = `${done} / ${total} items (${pct}%)`;
  else s = `${currentLabel} (${pct}%)`;
  if (typeof filePct === 'number' && filePct > 0) {
    s += ` — ${Math.round(filePct * 100)}%`;
  }
  return s;
}

// ─── Upload ───────────────────────────────────────────────────────────────────

function formatUploadRate(bytesPerSecond) {
  if (!bytesPerSecond || bytesPerSecond <= 0) return '—';
  return `${formatSize(bytesPerSecond)}/s`;
}

function formatUploadEta(seconds) {
  if (!Number.isFinite(seconds) || seconds <= 0) return '—';
  if (seconds < 60) return `${Math.ceil(seconds)}s`;
  const mins = Math.floor(seconds / 60);
  const secs = Math.ceil(seconds % 60);
  return `${mins}m ${secs}s`;
}

function createUploadTask(file, dir) {
  state.uploadTaskSeq += 1;
  return {
    id: `u${Date.now()}-${state.uploadTaskSeq}`,
    file,
    // Snapshot the target directory when the task is created: navigating
    // mid-upload must not redirect in-flight chunks into the wrong folder.
    dir: dir,
    name: file.name,
    size: file.size || 0,
    loaded: 0,
    status: 'uploading',
    speedBps: 0,
    etaSec: Infinity,
    xhr: null,
    error: '',
    lastSampleAt: 0,
    lastLoaded: 0,
    // Set when the user cancels a queued (not yet streaming) task; without it
    // the pool would still pick the task up and upload the file anyway.
    cancelled: false,
  };
}

function getUploadTask(id) {
  return state.uploadTasks.find(t => t.id === id);
}

function removeUploadTask(id) {
  state.uploadTasks = state.uploadTasks.filter(t => t.id !== id);
}

function cancelUploadTask(id) {
  const task = getUploadTask(id);
  if (!task || ['done', 'error', 'cancelled'].includes(task.status)) return;
  // Always record the intent: a queued task has no XHR yet, so aborting below
  // would never happen and the pool would transparently start it anyway.
  task.cancelled = true;
  if (task._ctl && !task._ctl.stop) {
    task._ctl.stop = 'cancel';
  }
  const xhrs = task.xhrs ? task.xhrs.slice() : [];
  if (task.xhr) xhrs.push(task.xhr);
  if (xhrs.length > 0) {
    xhrs.forEach(x => x.abort());
  } else {
    task.status = 'cancelled';
    removeUploadTask(task.id);
  }
  refreshUploadUI();
}

function buildUploadStatusText(task) {
  const pct = task.size > 0 ? Math.round((task.loaded / task.size) * 100) : 0;
  if (task.status === 'uploading') {
    return `${pct}% • ${formatSize(task.loaded)} / ${formatSize(task.size)} • ${formatUploadRate(task.speedBps)} • ETA ${formatUploadEta(task.etaSec)}`;
  }
  if (task.status === 'done') return `Completed • ${formatSize(task.size)}`;
  if (task.status === 'cancelled') return `Cancelled • ${pct}%`;
  if (task.status === 'error') return `Failed • ${task.error || 'Unknown error'}`;
  // Tasks are only ever created with status 'uploading' and settle into the
  // terminal states above; no other status exists.
  return '';
}

function renderUploadItems(container, tasks) {
  container.innerHTML = '';
  tasks.forEach(task => {
    const item = document.createElement('div');
    item.className = 'upload-item' +
      (task.status === 'done' ? ' done' : '') +
      (task.status === 'cancelled' ? ' cancelled' : '') +
      (task.status === 'error' ? ' error' : '');

    const head = document.createElement('div');
    head.className = 'upload-item-head';

    const name = document.createElement('div');
    name.className = 'upload-item-name';
    name.textContent = task.name;
    name.title = task.name;
    head.appendChild(name);

    if (task.status === 'uploading') {
      const cancel = document.createElement('button');
      cancel.type = 'button';
      cancel.className = 'upload-item-cancel';
      cancel.textContent = 'Cancel';
      cancel.addEventListener('click', () => cancelUploadTask(task.id));
      head.appendChild(cancel);
    }
    item.appendChild(head);

    const track = document.createElement('div');
    track.className = 'upload-item-track';
    const fill = document.createElement('div');
    fill.className = 'upload-item-fill';
    const pct = task.size > 0 ? Math.max(0, Math.min(100, (task.loaded / task.size) * 100)) : 0;
    fill.style.width = pct + '%';
    track.appendChild(fill);
    item.appendChild(track);

    const meta = document.createElement('div');
    meta.className = 'upload-item-meta';
    meta.textContent = buildUploadStatusText(task);
    item.appendChild(meta);

    container.appendChild(item);
  });
}

function renderUploadManager() {
  const sidebarList = document.getElementById('upload-list');
  const sidebarEmpty = document.getElementById('upload-list-empty');
  const modalList = document.getElementById('upload-modal-list');
  const modalEmpty = document.getElementById('upload-modal-empty');
  const countEl = document.getElementById('upload-count');
  if (!sidebarList || !sidebarEmpty || !modalList || !modalEmpty || !countEl) return;

  const tasks = [...state.uploadTasks].reverse();
  const activeCount = state.uploadTasks.filter(t => t.status === 'uploading').length;

  countEl.style.display = activeCount > 0 ? '' : 'none';
  countEl.textContent = `${activeCount} active`;

  if (tasks.length === 0) {
    sidebarEmpty.style.display = '';
    modalEmpty.style.display = '';
    sidebarList.innerHTML = '';
    modalList.innerHTML = '';
    return;
  }

  sidebarEmpty.style.display = 'none';
  modalEmpty.style.display = 'none';
  renderUploadItems(sidebarList, tasks);
  renderUploadItems(modalList, tasks);
}

function updateUploadProgressBar() {
  const bar = document.getElementById('upload-progress-bar');
  if (!bar) return;
  const active = state.uploadTasks.filter(t => t.status === 'uploading');
  if (active.length === 0) {
    bar.classList.remove('indeterminate');
    bar.style.display = 'none';
    bar.style.width = '0%';
    return;
  }
  const total = active.reduce((acc, t) => acc + Math.max(t.size, 1), 0);
  const loaded = active.reduce((acc, t) => acc + Math.min(Math.max(t.loaded, 0), Math.max(t.size, 1)), 0);
  const pct = Math.max(0, Math.min(100, (loaded / total) * 100));
  bar.classList.remove('indeterminate');
  bar.style.display = 'block';
  bar.style.width = pct + '%';
}

function scheduleUploadDirectoryReload() {
  if (state.uploadRefreshTimer) return;
  state.uploadRefreshTimer = setTimeout(() => {
    state.uploadRefreshTimer = null;
    loadDirectory(state.currentPath);
  }, 350);
}

// Shared UI refresh for upload state changes.
function refreshUploadUI() {
  renderUploadManager();
  updateUploadProgressBar();
}

// Exponential moving average for upload speed + ETA.
function updateUploadSpeed(task, loaded) {
  const now = Date.now();
  task.loaded = loaded;
  const dt = (now - task.lastSampleAt) / 1000;
  const dBytes = loaded - task.lastLoaded;
  if (dt > 0 && dBytes >= 0) {
    const instant = dBytes / dt;
    task.speedBps = task.speedBps > 0 ? (task.speedBps * 0.7 + instant * 0.3) : instant;
  }
  task.lastSampleAt = now;
  task.lastLoaded = loaded;
  task.etaSec = task.speedBps > 0 ? (Math.max(task.size - loaded, 0) / task.speedBps) : Infinity;
}

function runUploadTask(task) {
  // The user cancelled while this task was still queued (no XHR existed yet).
  if (task.cancelled) return Promise.resolve('cancelled');
  if (task.file && task.file.size >= PARALLEL_MIN_FILE_SIZE && typeof task.file.slice === 'function') {
    return uploadFileChunked(task);
  }
  return uploadFileSingle(task);
}

// finishUploadTask is the single settlement path for a simple (non-chunked)
// upload: every terminal XHR event (load/error/timeout/abort) records the
// status, cleans up the live XHR reference, updates the UI and resolves the
// task promise — so the four branches cannot drift apart.
function finishUploadTask(task, resolve, status, error) {
  task.xhrs = null;
  task.status = status;
  if (error) task.error = error;
  if (status === 'done') {
    task.loaded = task.size;
    task.etaSec = 0;
  }
  if (status === 'done' || status === 'cancelled') {
    removeUploadTask(task.id);
  }
  if (status === 'done') scheduleUploadDirectoryReload();
  refreshUploadUI();
  resolve(status);
}

function uploadFileSingle(task) {
  return new Promise(resolve => {
    const fd = new FormData();
    fd.append('file', task.file);

    const xhr = new XMLHttpRequest();
    task.xhrs = [xhr];
    task.status = 'uploading';
    task.lastSampleAt = Date.now();
    task.lastLoaded = 0;

    xhr.open('POST', '/api/upload?path=' + encodeURIComponent(task.dir));

    xhr.timeout = UPLOAD_REQUEST_TIMEOUT_MS;
    xhr.upload.onprogress = e => {
      if (!e.lengthComputable) return;
      updateUploadSpeed(task, e.loaded);
      refreshUploadUI();
    };
    xhr.ontimeout = () => finishUploadTask(task, resolve, 'error', 'Upload timed out');
    xhr.onload = () => {
      if (xhr.status === 200) { finishUploadTask(task, resolve, 'done', null); return; }
      let msg = xhr.statusText || 'Upload failed';
      try { msg = JSON.parse(xhr.responseText).error || msg; } catch { /* keep msg */ }
      finishUploadTask(task, resolve, 'error', msg);
    };
    xhr.onerror = () => finishUploadTask(task, resolve, 'error', 'Network error');
    xhr.onabort = () => finishUploadTask(task, resolve, 'cancelled', null);
    xhr.send(fd);
  });
}

async function uploadFileChunked(task) {
  return new Promise(resolve => {
    const file = task.file;
    const size = file.size;
    const chunkCount = Math.ceil(size / PARALLEL_CHUNK_SIZE);
    const streams = Math.min(PARALLEL_STREAMS_PER_FILE, chunkCount);

    const chunkPos = new Array(chunkCount).fill(0);
    const results = new Array(chunkCount);
    const xhrs = [];
    const ctl = { stop: '' };
    task.xhrs = xhrs;
    task._ctl = ctl;
    task.status = 'uploading';
    task.lastSampleAt = Date.now();
    task.lastLoaded = 0;

    let next = 0;

    const refresh = () => {
      let loaded = 0;
      for (let i = 0; i < chunkCount; i++) loaded += chunkPos[i];
      updateUploadSpeed(task, loaded);
      refreshUploadUI();
    };

    const abortActiveXhrs = () => {
      const copy = xhrs.slice();
      xhrs.length = 0;
      copy.forEach(x => x.abort());
    };

    const uploadChunk = index => {
      return new Promise(chunkResolve => {
        const start = index * PARALLEL_CHUNK_SIZE;
        const end = Math.min(start + PARALLEL_CHUNK_SIZE, size);
        const xhr = new XMLHttpRequest();
        xhrs.push(xhr);
        const fd = new FormData();
        fd.append('file', file.slice(start, end), file.name);

        xhr.open('POST',
          '/api/upload?path=' + encodeURIComponent(task.dir) +
          '&offset=' + start + '&total=' + size);

        xhr.timeout = UPLOAD_REQUEST_TIMEOUT_MS;
        xhr.upload.onprogress = e => {
          if (!e.lengthComputable) return;
          chunkPos[index] = e.loaded;
          refresh();
        };
        xhr.ontimeout = () => {
          task.status = 'error';
          task.error = 'Upload timed out';
          if (!ctl.stop) ctl.stop = 'fail';
          abortActiveXhrs();
          refresh();
          chunkResolve('error');
        };
        xhr.onload = () => {
          if (xhr.status === 200) {
            chunkPos[index] = end - start;
            refresh();
            chunkResolve('done');
            return;
          }
          task.status = 'error';
          try { task.error = JSON.parse(xhr.responseText).error || xhr.statusText; }
          catch { task.error = xhr.statusText || 'Upload failed'; }
          if (!ctl.stop) ctl.stop = 'fail';
          abortActiveXhrs();
          refresh();
          chunkResolve('error');
        };
        xhr.onerror = () => {
          task.status = 'error';
          task.error = 'Network error';
          if (!ctl.stop) ctl.stop = 'fail';
          abortActiveXhrs();
          refresh();
          chunkResolve('error');
        };
        xhr.onabort = () => {
          chunkResolve('cancelled');
        };
        xhr.send(fd);
      });
    };

    (async () => {
      const worker = async () => {
        while (next < chunkCount) {
          const i = next++;
          await acquireUploadStream();
          // Check both the control object and the task flag: the latter stays
          // true for a queued-then-started cancellation.
          if (ctl.stop || task.cancelled) {
            releaseUploadStream();
            results[i] = (ctl.stop === 'cancel' || task.cancelled) ? 'cancelled' : 'error';
            continue;
          }
          results[i] = await uploadChunk(i);
          releaseUploadStream();
        }
      };
      const workers = Array.from({ length: streams }, () => worker());
      await Promise.all(workers);

      const hasError = results.includes('error') || ctl.stop === 'fail';
      const hasCancelled = results.includes('cancelled') || ctl.stop === 'cancel';
      if (hasCancelled && !hasError) {
        task.status = 'cancelled';
        removeUploadTask(task.id);
        refreshUploadUI();
        resolve('cancelled');
      } else if (hasError) {
        task.status = 'error';
        refresh();
        resolve('error');
      } else {
        task.status = 'done';
        task.loaded = size;
        task.etaSec = 0;
        removeUploadTask(task.id);
        scheduleUploadDirectoryReload();
        refreshUploadUI();
        resolve('done');
      }
    })().catch(() => {
      // Not reachable through any of the XHR branches (those settle the outer
      // promise directly). This only fires on a sync throw such as a failing
      // file.slice() during chunk assembly — without it Promise.all(workers)
      // would reject and the pool would hang in "uploading" forever. Abort any
      // in-flight chunk XHRs so the server does not receive a partial tail.
      abortActiveXhrs();
      task.status = 'error';
      task.error = 'Upload failed';
      refreshUploadUI();
      resolve('error');
    });
  });
}

const MAX_CONCURRENT_UPLOADS = 3;

// If a request makes no forward progress for this long it is treated as
// stalled: aborted and the upload fails cleanly instead of "uploading" forever.
const UPLOAD_REQUEST_TIMEOUT_MS = 300000;

// Large files are split into chunks and uploaded over concurrent HTTP
// sessions so a single file saturates more than one TCP connection. Small
// files keep the cheap single-stream path.
const PARALLEL_CHUNK_SIZE = 16 * 1024 * 1024;       // 16 MiB per chunk
const PARALLEL_MIN_FILE_SIZE = 32 * 1024 * 1024;    // only chunk files >= 32 MiB
const PARALLEL_STREAMS_PER_FILE = 6;                // concurrent streams per file

// Browser (and especially Safari/WebKit) cap HTTP/1.1 connections per host;
// this global budget keeps the total number of concurrent upload sessions
// under that cap so parallel chunks never starve the connection pool.
const MAX_UPLOAD_STREAMS = 6;
const uploadStreamWaiters = [];
let uploadStreamActive = 0;

function acquireUploadStream() {
  return new Promise(resolve => {
    if (uploadStreamActive < MAX_UPLOAD_STREAMS) {
      uploadStreamActive++;
      resolve();
    } else {
      uploadStreamWaiters.push(resolve);
    }
  });
}

function releaseUploadStream() {
  const pending = uploadStreamWaiters.shift();
  if (pending) {
    pending();
  } else {
    uploadStreamActive--;
  }
}

// Runs upload tasks with a bounded number of simultaneous transfers. Safari
// caps HTTP/1.1 connections per host, and firing every large upload at once
// can starve the pool and leave transfers suspended.
async function runUploadPool(tasks, limit) {
  const results = new Array(tasks.length);
  let next = 0;
  async function worker() {
    while (next < tasks.length) {
      const i = next++;
      results[i] = await runUploadTask(tasks[i]);
    }
  }
  const workers = Array.from({ length: Math.min(limit, tasks.length) }, () => worker());
  await Promise.all(workers);
  return results;
}

async function uploadFiles(files) {
  if (!files || files.length === 0) return;
  const tasks = files.map(f => createUploadTask(f, state.currentPath));
  state.uploadTasks.push(...tasks);
  refreshUploadUI();

  const results = await runUploadPool(tasks, MAX_CONCURRENT_UPLOADS);
  const done = results.filter(r => r === 'done').length;
  const cancelled = results.filter(r => r === 'cancelled').length;
  const failed = results.filter(r => r === 'error').length;
  if (done > 0) {
    toast(done + ' file' + (done === 1 ? '' : 's') + ' uploaded', 'success');
  }
  if (cancelled > 0) {
    toast(cancelled + ' upload' + (cancelled === 1 ? '' : 's') + ' cancelled', 'info');
  }
  if (failed > 0) {
    toast(failed + ' upload' + (failed === 1 ? '' : 's') + ' failed', 'error');
  }
  if (done > 0) {
    scheduleUploadDirectoryReload();
  }
  const maxHistory = 50;
  if (state.uploadTasks.length > maxHistory) {
    state.uploadTasks = state.uploadTasks.slice(state.uploadTasks.length - maxHistory);
    refreshUploadUI();
  }
}

// ─── Mkdir ────────────────────────────────────────────────────────────────────
function openMkdirModal() {
  document.getElementById('mkdir-name').value = '';
  openModalFocus('modal-mkdir', 'mkdir-name');
}

async function doMkdir() {
  const name = document.getElementById('mkdir-name').value.trim();
  if (!name) { toast('Enter a folder name', 'error'); return; }
  try {
    // apiPost returns null when the session expired (it redirects to login);
    // the success braids below must not run for work that never happened.
    const data = await apiPost('/api/mkdir', { path: state.currentPath, name });
    if (!data) return;
    toast('Created folder: ' + name, 'success');
    closeModal('modal-mkdir');
    loadDirectory(state.currentPath);
  } catch (e) {
    toastError(e, 'Error');
  }
}

// ─── New File ─────────────────────────────────────────────────────────────────
function openNewFileModal() {
  document.getElementById('new-file-name').value = '';
  openModalFocus('modal-new-file', 'new-file-name');
}

async function doNewFile() {
  const name = document.getElementById('new-file-name').value.trim();
  if (!name) { toast('Enter a file name', 'error'); return; }
  try {
    // The backend refuses the request when the file already exists or cannot be
    // created (e.g. the directory is not writable), so the modal stays open with
    // the error visible for the user to correct.
    const data = await apiPost('/api/create-file', { path: state.currentPath, name });
    // data is null after an auth failure (apiPost returns null on 401 and
    // redirects); do not toast success or reload against the login page.
    if (!data) return;
    closeModal('modal-new-file');
    toast('Created file: ' + name, 'success');
    await loadDirectory(state.currentPath);
    openEditor({ path: data.path, name });
  } catch (e) {
    toastError(e, 'Error');
  }
}

// ─── Change password ─────────────────────────────────────────────────────────
function openPasswordModal() {
  clearPasswordFields();
  openModalFocus('modal-password', 'pwd-current');
}

function clearPasswordFields() {
  ['pwd-current', 'pwd-new', 'pwd-new2'].forEach(id => {
    document.getElementById(id).value = '';
  });
}

async function submitPasswordChange() {
  const current = document.getElementById('pwd-current').value;
  const pw = document.getElementById('pwd-new').value;
  const pw2 = document.getElementById('pwd-new2').value;
  if (!current) { toast('Enter your current password', 'error'); return; }
  if (!pw) { toast('Enter a new password', 'error'); return; }
  if (pw !== pw2) { toast('Passwords do not match', 'error'); return; }
  try {
    const data = await apiPost('/api/change-password', { current_password: current, new_password: pw });
    if (!data) return;
    toast('Password updated', 'success');
    closeModal('modal-password');
  } catch (e) {
    toastError(e, 'Failed to update password');
  }
}

// ─── Move ─────────────────────────────────────────────────────────────────────

// Per-modal browsed path
const folderBrowserPath = { move: '/', copy: '/' };
const folderBrowserRequest = { move: 0, copy: 0 };

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
  const request = ++folderBrowserRequest[mode];
  folderBrowserPath[mode] = path;
  const listEl  = document.getElementById(mode + '-browser-list');
  const crumbEl = document.getElementById(mode + '-browser-crumb');
  const upBtn   = document.getElementById(mode + '-browser-up');

  crumbEl.textContent = path;
  upBtn.disabled = (path === '/');

  listEl.innerHTML = '<div class="folder-browser-empty">Loading…</div>';
  try {
    const entries = await apiList(path, state.showDotfiles);
    // Ignore stale responses from a superseded navigation so the list and
    // crumb/path state never disagree after rapid folder clicks.
    if (request !== folderBrowserRequest[mode] || folderBrowserPath[mode] !== path) return;
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
  const parent = parentPath(cur);
  document.getElementById(mode + '-dst').value = parent;
  loadFolderBrowser(mode, parent);
}

async function ensureBatchDestinationDirectory(dst) {
  try {
    await apiPost('/api/mkdir', { path: dst });
  } catch (mkdirErr) {
    // If mkdir failed because the destination already exists, ensure it is a directory.
    try {
      await apiList(dst, false);
    } catch (_) {
      throw mkdirErr;
    }
  }
}

// openTransferModal opens the move or copy dialog for the given mode.
function openTransferModal(mode, pathOrPaths) {
  const srcKey = mode + 'Src';
  state[srcKey] = Array.isArray(pathOrPaths) ? pathOrPaths : [pathOrPaths];
  const initPath = state.currentPath;
  const dstId = mode + '-dst';
  document.getElementById(dstId).value = initPath;
  renderModalFavourites(mode);
  openModalFocus('modal-' + mode, dstId);
  loadFolderBrowser(mode, initPath);
}

function openMoveModal(pathOrPaths) { openTransferModal('move', pathOrPaths); }

// readNdjsonStream consumes a streaming NDJSON response body, calling
// onMessage(msg) for each parsed JSON line. Malformed lines are ignored.
async function readNdjsonStream(response, onMessage) {
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const lines = buffer.split('\n');
    buffer = lines.pop() || '';
    for (const line of lines) {
      if (!line.trim()) continue;
      try {
        onMessage(JSON.parse(line));
      } catch (e) { /* ignore malformed lines */ }
    }
  }
  if (buffer.trim()) {
    try { onMessage(JSON.parse(buffer)); } catch (e) {}
  }
}

// transferPercent converts an NDJSON progress line into a single 0-100
// percentage: multi-item batches count finished items; a single item with a
// per-file percentage (wipe/extract) prefers the file's own progress.
function transferPercent(done, total, filePct) {
  return total === 1 && typeof filePct === 'number' ? filePct * 100 : (total > 0 ? (done / total) * 100 : 0);
}

// readNdjsonStreamAndResult consumes a streaming NDJSON response, dispatching
// every parsed message to onProgress, and returns the final status message. A
// non-OK HTTP response or a final "error" event is thrown here, so callers
// don't each re-implement the fetch-error + trailing-error handling.
async function readNdjsonStreamAndResult(r, onProgress) {
  if (isUnauthorized(r)) return null;
  if (!r.ok) {
    const data = await r.json().catch(() => ({}));
    throw errorFromData(data, r.statusText);
  }
  let last = null;
  await readNdjsonStream(r, (msg) => {
    if (msg.type === 'progress') {
      if (onProgress) onProgress(msg);
    } else {
      last = msg;
    }
  });
  if (last && last.type === 'error') {
    const err = new Error(last.message || last.error || 'Operation failed');
    if (typeof last.code === 'string') err.code = last.code;
    if (Array.isArray(last.conflicts)) err.conflicts = last.conflicts;
    throw err;
  }
  return last || { status: 'ok' };
}

// readErrorResponse converts a failed response into an Error the same way
// everywhere (a non-JSON body still surfaces the HTTP status text).
async function readErrorResponse(r) {
  const data = await r.json().catch(() => ({}));
  throw errorFromData(data, r.statusText);
}

// downloadUrl builds the download endpoint for a path.
function downloadUrl(path) {
  return '/api/download?path=' + encodeURIComponent(path);
}

// readFileContent loads a file's text content (null on session expiry).
function readFileContent(path) {
  return apiGet('/api/read?path=' + encodeURIComponent(path));
}

// triggerDownload starts a browser download for url with the given filename.
// The anchor is appended/removed explicitly so the click always registers, and
// callers that must revoke an object URL do so after the click has started.
function triggerDownload(url, filename) {
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
}

// streamFileOp posts a single move/copy batch and consumes the NDJSON
// progress stream. onProgress(done, total, current) fires as each item is
// processed. Throws an Error on the final error event (or an HTTP error).
async function streamFileOp(url, srcs, dst, onConflict, controller, onProgress) {
  const payload = srcs.length === 1
    ? { src: srcs[0], dst, on_conflict: onConflict }
    : { srcs, dst, on_conflict: onConflict };
  const r = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
    signal: controller.signal,
  });
  const result = await readNdjsonStreamAndResult(r, (msg) => {
    if (onProgress) onProgress(msg.done, msg.total, msg.current, typeof msg.filePct === 'number' ? msg.filePct : null);
  });
  // readNdjsonStreamAndResult returns null when the session expired mid-stream
  // (the client is about to be redirected to the login page). Treat it as an
  // abort so the caller does NOT toast a fake "Moved successfully" for work
  // that never happened.
  if (result == null) throw makeAbortError('Session expired');
  return result;
}

// resolveConflictBatch streams move/copy batches to url, showing progress via
// onProgress(done, total, current) and retrying on destination conflicts. When
// the user checks "apply to all subsequent conflicts" the chosen strategy is
// reapplied to the whole remaining batch; otherwise only the conflicting
// item(s) are resolved individually and the rest is retried.
async function resolveConflictBatch(url, verb, srcs, dst, controller, onProgress) {
  let pending = srcs.slice();
  let baseDone = 0;
  const total = srcs.length;
  let onConflict = 'error';
  while (pending.length > 0) {
    const start = baseDone;
    const subProgress = (done, subTotal, current, filePct) => {
      if (!onProgress) return;
      const absDone = subTotal > 0 ? start + Math.min(done, subTotal) : start;
      onProgress(absDone, total, current, filePct);
    };
    try {
      await streamFileOp(url, pending, dst, onConflict, controller, subProgress);
      return;
    } catch (e) {
      if (e.name === 'AbortError') throw e;
      if (!isDestinationConflictError(e) || onConflict !== 'error') throw e;
      // The single-item endpoint reports a conflict without a list of specific
      // paths, so fall back to treating the whole pending set as conflicted.
      const conflictBases = new Set((e.conflicts || []).map(basename));
      const conflicted = conflictBases.size > 0
        ? pending.filter(s => conflictBases.has(basename(s)))
        : pending.slice();
      // Show the first actually-conflicting item when we know it.
      const shown = conflicted[0] || pending[0];
      const resolution = await askConflictResolution(verb, shown, dst);
      if (resolution.action === 'cancel') throw makeAbortError(verb + ' cancelled');
      if (resolution.applyToAll) {
        onConflict = resolution.action;
        continue;
      }
      // Resolve only the reported conflicts, then retry the remaining sources.
      for (const src of conflicted) {
        await streamFileOp(url, [src], dst, resolution.action, controller);
        baseDone++;
      }
      pending = conflictBases.size > 0
        ? pending.filter(s => !conflictBases.has(basename(s)))
        : [];
    }
  }
}

// setupCopyModalProgress replaces the copy modal body with an inline progress
// UI. Returns { render(done,total,current,filePct) } and restore() to put the
// original modal content back.
function setupCopyModalProgress() {
  const copyModal = document.getElementById('modal-copy');
  const copyTitle = copyModal.querySelector('.modal-title');
  const copyBody = copyModal.querySelector('.modal-body');
  const copyFooter = copyModal.querySelector('.modal-footer');

  const origTitle = copyTitle.textContent;
  const origBodyHTML = copyBody.innerHTML;
  const origFooterHTML = copyFooter.innerHTML;

  copyTitle.textContent = 'Copying…';
  copyBody.innerHTML = '<div style="text-align:center;padding:20px 24px"><div id="copy-progress-status" style="margin-bottom:10px;font-size:14px;color:var(--text)">Starting...</div><div style="height:6px;background:var(--border);border-radius:3px;overflow:hidden;margin-bottom:6px"><div id="copy-progress-fill" class="indeterminate" style="width:100%;height:100%;background:var(--accent);border-radius:3px"></div></div><div id="copy-progress-percent" style="font-size:12px;color:var(--text-dim)"></div></div>';
  copyFooter.innerHTML = '<button class="btn" id="copy-progress-hide">Hide</button><button class="btn danger" id="copy-progress-cancel">Cancel</button>';

  document.getElementById('copy-progress-hide').addEventListener('click', () => {
    closeModal('modal-copy', false);
  });
  document.getElementById('copy-progress-cancel').addEventListener('click', () => {
    if (state.opAbort) state.opAbort();
  });

  return {
    render(done, total, current, filePct) {
      const fill = document.getElementById('copy-progress-fill');
      if (!fill) return;
      const pct = transferPercent(done, total, filePct);
      const sub = formatTransferProgress(done, total, current || '', filePct);
      fill.className = '';
      fill.style.width = pct + '%';
      document.getElementById('copy-progress-percent').textContent = Math.round(pct) + '%';
      document.getElementById('copy-progress-status').textContent = sub;
    },
    restore() {
      copyTitle.textContent = origTitle;
      copyBody.innerHTML = origBodyHTML;
      copyFooter.innerHTML = origFooterHTML;
    },
  };
}

// doFileTransfer performs a move or copy batch depending on mode ('move'|'copy').
async function doFileTransfer(mode) {
  const isMove = mode === 'move';
  // Re-entrancy guard: a double confirmation (dbl-click / stray Enter) must
  // not start a second transfer over the single opAbort handle. Both modes
  // share one in-flight operation, so either flag blocks both.
  if (state.moveInProgress || state.copyInProgress) {
    toast('An operation is already in progress', 'info');
    return;
  }
  const srcsKey = isMove ? 'moveSrc' : 'copySrc';
  const progressKey = isMove ? 'moveInProgress' : 'copyInProgress';
  const label = isMove ? 'Moving…' : 'Copying…';
  const past = isMove ? 'Moved' : 'Copied';
  const verb = isMove ? 'Move' : 'Copy';
  const endpoint = isMove ? '/api/move' : '/api/copy';
  const dstId = isMove ? 'move-dst' : 'copy-dst';
  const modalId = isMove ? 'modal-move' : 'modal-copy';
  const confirmId = isMove ? 'move-confirm' : 'copy-confirm';

  const dst = document.getElementById(dstId).value.trim();
  if (!dst) { toast('Enter a destination path', 'error'); return; }
  const srcs = Array.isArray(state[srcsKey]) ? state[srcsKey] : [state[srcsKey]];
  if (srcs.length === 0) { toast(`No items to ${verb.toLowerCase()}`, 'error'); return; }
  // Ensure the destination directory exists for a single item too — a plain
  // move/copy into a typo'd or not-yet-created folder would otherwise fail
  // server-side with a generic error instead of creating it.
  try {
    await ensureBatchDestinationDirectory(dst);
  } catch (e) {
    toastError(e, verb + ' failed');
    return;
  }

  const btn = document.getElementById(confirmId);
  setTopProgress(true);
  btn.disabled = true;

  const controller = new AbortController();
  state.opAbort = () => controller.abort();
  state[progressKey] = true;

  // Copy shows inline progress inside the modal; move uses the op progress modal.
  const copyProgress = isMove ? null : setupCopyModalProgress();
  if (isMove) {
    closeModal(modalId, false);
    showOpProgress(label, 0, formatTransferProgress(0, srcs.length, basename(srcs[0])));
    openOpProgressModal();
  }

  try {
    const progress = (done, total, current, filePct) => {
      const pct = transferPercent(done, total, filePct);
      const sub = formatTransferProgress(done, total, current || basename(srcs[0]), filePct);
      showOpProgress(label, pct, sub);
      if (copyProgress) copyProgress.render(done, total, current, filePct);
    };
    await resolveConflictBatch(endpoint, verb, srcs, dst, controller, progress);
    progress(srcs.length, srcs.length, basename(srcs[srcs.length - 1]));
    toast(srcs.length === 1 ? `${past} successfully` : `${past} ${srcs.length} items`, 'success');
    state.selectedFiles.clear();
    updateSelectionButtons();
    await loadProtectedPaths();
    loadDirectory(dst);
  } catch (e) {
    if (e.name === 'AbortError') {
      toast(`${verb} cancelled`, 'info');
    } else {
      toastError(e, verb + ' failed');
    }
  } finally {
    state[progressKey] = false;
    hideOpProgress();
    setTopProgress(false);
    btn.disabled = false;
    if (copyProgress) {
      copyProgress.restore();
      closeModal(modalId, false);
    }
  }
}

function doMove() { return doFileTransfer('move'); }
function doCopy() { return doFileTransfer('copy'); }

// ─── Zip Download ─────────────────────────────────────────────────────────────
async function downloadZip(paths) {
  setTopProgress(true);
  try {
    const name = paths.length === 1 ? basename(paths[0]) + '.zip' : 'download.zip';
    const r = await fetch('/api/zip-download', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ paths }),
    });
    if (isUnauthorized(r)) return;
    if (!r.ok) throw await readErrorResponse(r);
    const blob = await r.blob();
    const url = URL.createObjectURL(blob);
    triggerDownload(url, name);
    // Revoke only after the click has had a chance to start reading the blob;
    // revoking synchronously can abort the download in some browsers.
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  } catch (e) {
    toastError(e, 'Download failed');
  } finally {
    setTopProgress(false);
  }
}

function openCopyModal(pathOrPaths) { openTransferModal('copy', pathOrPaths); }

// ─── Text Editor ──────────────────────────────────────────────────────────────
async function openEditor(entry) {
  // seq fences concurrent opens: a slow /api/read for an earlier file must not
  // overwrite the textarea contents of a file opened meanwhile (same race as
  // the preview modal, which already uses _previewSeq).
  const seq = ++_editorSeq;
  state.editorPath = entry.path;
  document.getElementById('editor-title').textContent = 'Edit — ' + entry.name;
  document.getElementById('editor-textarea').value = 'Loading…';
  openModal('modal-editor');
  try {
    const data = await readFileContent(entry.path);
    if (seq !== _editorSeq) return; // superseded by a newer editor open
    document.getElementById('editor-textarea').value = data && data.content ? data.content : '';
  } catch (e) {
    if (seq !== _editorSeq) return;
    document.getElementById('editor-textarea').value = '';
    toastError(e, 'Could not read file');
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
    toastError(e, 'Save failed');
  }
}

// ─── Markdown Renderer ─────────────────────────────────────────────────────────

function renderMarkdown(text) {
  // Parse RAW lines: block quote markers ("> ") and code fences must be
  // recognised before any escaping happens, otherwise the ">" becomes "&gt;"
  // and blockquotes silently render as a flat paragraph.
  const lines = text.split('\n');
  const blocks = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    if (line.startsWith('```')) {
      const fence = line.match(/^`{3,}\s*(\w*)/);
      const lang = fence ? fence[1] : '';
      const codeLines = [];
      i++;
      while (i < lines.length && !lines[i].startsWith('```')) {
        codeLines.push(lines[i]);
        i++;
      }
      i++;
      const code = codeLines.join('\n');
      blocks.push({ type: 'code', content: code, lang });
      continue;
    }

    if (/^#{1,6}\s/.test(line)) {
      const level = line.match(/^(#{1,6})\s/)[1].length;
      const content = line.slice(level + 1);
      blocks.push({ type: 'heading', level, content });
      i++;
      continue;
    }

    if (/^>[ ]?/.test(line)) {
      const quoteLines = [];
      while (i < lines.length && /^>[ ]?/.test(lines[i])) {
        quoteLines.push(lines[i].replace(/^>[ ]?/, ''));
        i++;
      }
      blocks.push({ type: 'blockquote', content: quoteLines.join('\n') });
      continue;
    }

    if (/^[-*+]\s/.test(line)) {
      const items = [];
      while (i < lines.length && /^[-*+]\s/.test(lines[i])) {
        items.push(lines[i].replace(/^[-*+]\s/, ''));
        i++;
      }
      blocks.push({ type: 'ulist', items });
      continue;
    }

    if (/^\d+\.\s/.test(line)) {
      const items = [];
      while (i < lines.length && /^\d+\.\s/.test(lines[i])) {
        items.push(lines[i].replace(/^\d+\.\s/, ''));
        i++;
      }
      blocks.push({ type: 'olist', items });
      continue;
    }

    if (/^(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      blocks.push({ type: 'hr' });
      i++;
      continue;
    }

    if (line.trim() === '') {
      i++;
      continue;
    }

    if (line.includes('|') && i + 1 < lines.length && /^\|?[\s:-]+(?:\|[\s:-]+)+\|?$/.test(lines[i + 1])) {
      const rows = [];
      while (i < lines.length && lines[i].includes('|')) {
        rows.push(lines[i]);
        i++;
      }
      const cells = row => {
        const parts = row.split('|');
        if (row.trim().startsWith('|')) return parts.slice(1, -1).map(c => c.trim());
        return parts.map(c => c.trim());
      };
      const header = cells(rows[0]);
      const body = rows.slice(2).map(row => cells(row));
      blocks.push({ type: 'table', header, body });
      continue;
    }

    const paraLines = [];
    while (i < lines.length && lines[i].trim() !== '' && !/^#{1,6}\s/.test(lines[i]) && !/^>[ ]?/.test(lines[i]) && !/^[-*+]\s/.test(lines[i]) && !/^\d+\.\s/.test(lines[i]) && !lines[i].startsWith('```') && !/^(-{3,}|\*{3,}|_{3,})\s*$/.test(lines[i])) {
      paraLines.push(lines[i]);
      i++;
    }
    if (paraLines.length > 0) {
      blocks.push({ type: 'paragraph', content: paraLines.join('\n') });
    }
  }

  function inline(text) {
    // Tokenise links, images and code spans BEFORE emphasis so `*`/`_` inside
    // a URL or alt text can never be wrapped in <em>/<strong>, and stash the
    // already-escaped HTML so labels are never double-escaped. The remaining
    // plain text is escaped exactly once.
    const tokens = [];
    const stash = s => '\u0000' + (tokens.push(s) - 1) + '\u0000';
    let t = text
      .replace(/`([^`]+)`/g, (m, c) => stash('<code>' + escapeText(c) + '</code>'))
      .replace(/!\[([^\]]*)\]\(([^)]+)\)/g, (m, alt, url) => stash('<img src="' + safeMarkdownUrl(url) + '" alt="' + escapeAttr(alt) + '">'))
      .replace(/\[([^\]]+)\]\(([^)]+)\)/g, (m, label, url) => stash('<a href="' + safeMarkdownUrl(url) + '" target="_blank" rel="noopener">' + escapeText(label) + '</a>'));
    t = escapeText(t)
      .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
      .replace(/__([^_]+)__/g, '<strong>$1</strong>')
      .replace(/\*([^*]+)\*/g, '<em>$1</em>')
      .replace(/_([^_]+)_/g, '<em>$1</em>');
    return t.replace(/\u0000(\d+)\u0000/g, (m, n) => tokens[+n]);
  }

  const html = blocks.map(b => {
    switch (b.type) {
      case 'code':
        return `<pre><code${b.lang ? ` class="language-${escapeAttr(b.lang)}"` : ''}>${escapeText(b.content)}</code></pre>`;
      case 'heading':
        return `<h${b.level}>${inline(b.content)}</h${b.level}>`;
      case 'blockquote':
        return `<blockquote>${inline(b.content)}</blockquote>`;
      case 'ulist':
        return `<ul>${b.items.map(item => `<li>${inline(item)}</li>`).join('')}</ul>`;
      case 'olist':
        return `<ol>${b.items.map(item => `<li>${inline(item)}</li>`).join('')}</ol>`;
      case 'hr':
        return '<hr>';
      case 'table':
        return `<table><thead><tr>${b.header.map(h => `<th>${inline(h)}</th>`).join('')}</tr></thead><tbody>${b.body.map(row => `<tr>${row.map(c => `<td>${inline(c)}</td>`).join('')}</tr>`).join('')}</tbody></table>`;
      case 'paragraph':
        return `<p>${inline(b.content)}</p>`;
      default:
        return '';
    }
  }).join('\n');

  return html;
}

// ─── Preview ──────────────────────────────────────────────────────────────────
const PREVIEWABLE_HINTS = new Set(['image', 'video', 'audio', 'text', 'code', 'pdf', 'markdown']);

function buildPreviewList() {
  return getFilteredEntries().filter(e => !e.is_dir && PREVIEWABLE_HINTS.has(e.mime_hint));
}

function updatePreviewNav() {
  const prevBtn = document.getElementById('preview-prev');
  const nextBtn = document.getElementById('preview-next');
  const hasPrev = state.previewIndex > 0;
  const hasNext = state.previewIndex >= 0 && state.previewIndex < state.previewList.length - 1;
  prevBtn.style.display = hasPrev ? '' : 'none';
  nextBtn.style.display = hasNext ? '' : 'none';
  prevBtn.disabled = !hasPrev;
  nextBtn.disabled = !hasNext;
}

function navigatePreview(dir) {
  const newIdx = state.previewIndex + dir;
  if (newIdx < 0 || newIdx >= state.previewList.length) return;
  openPreview(state.previewList[newIdx]);
}

async function deleteCurrentPreview() {
  const current = state.previewList[state.previewIndex];
  if (!current) return;
  if (isProtected(current.path)) {
    toast('This path is protected and cannot be deleted', 'error');
    return;
  }

  const deleted = await deleteFiles([current.path]);
  if (!deleted) return;

  state.entries = state.entries.filter(e => e.path !== current.path);
  state.previewList = buildPreviewList();
  if (state.previewList.length === 0) {
    closeModal('modal-preview');
    return;
  }

  const nextIdx = Math.min(state.previewIndex, state.previewList.length - 1);
  openPreview(state.previewList[nextIdx]);
}

// wirePreviewButtons wires the preview modal's shared header controls (title,
// download, edit, move, copy, delete) for the given entry. onEdit may be null
// to hide the edit button.
function wirePreviewButtons(entry, onEdit) {
  const title = document.getElementById('preview-title');
  const dlBtn = document.getElementById('preview-download');
  const editBtn = document.getElementById('preview-edit');
  const moveBtn = document.getElementById('preview-move');
  const copyBtn = document.getElementById('preview-copy');
  const deleteBtn = document.getElementById('preview-delete');

  title.textContent = entry.name;
  dlBtn.href = downloadUrl(entry.path);
  dlBtn.download = entry.name;
  editBtn.style.display = onEdit ? '' : 'none';
  if (onEdit) editBtn.onclick = () => { closeModal('modal-preview'); onEdit(); };
  moveBtn.onclick = () => { closeModal('modal-preview'); openMoveModal(entry.path); };
  copyBtn.onclick = () => { closeModal('modal-preview'); openCopyModal(entry.path); };
  deleteBtn.onclick = deleteCurrentPreview;
}

// previewTextFile shows a text/code file in the preview modal container.
async function previewTextFile(entry, seq) {
  const data = await readFileContent(entry.path);
  if (data == null || (seq !== undefined && seq !== _previewSeq)) return;
  const pre = document.createElement('pre');
  pre.textContent = data.content;
  openPreviewElement(pre);
}

function streamUrlFor(entry) {
  return '/api/stream?path=' + encodeURIComponent(entry.path);
}

// openPreviewElement appends the ready-made preview element and shows the modal.
function openPreviewElement(el) {
  document.getElementById('preview-container').appendChild(el);
  openModal('modal-preview');
}

async function openPreview(entry) {
  const seq = ++_previewSeq;
  const list = buildPreviewList();
  const idx = list.findIndex(e => e.path === entry.path);
  state.previewList = list;
  state.previewIndex = idx;

  const container = document.getElementById('preview-container');
  const hint = entry.mime_hint;

  let onEdit = null;
  if (hint === 'video' && state.ffmpegAvailable) {
    onEdit = () => openVideoEditor(entry);
  } else if (['text', 'code', 'file', 'markdown'].includes(hint)) {
    // Open the same text editor used by the file list "Edit" action.
    onEdit = () => openEditor(entry);
  }
  wirePreviewButtons(entry, onEdit);

  updatePreviewNav();

  container.innerHTML = '';

  if (hint === 'image') {
    const img = document.createElement('img');
img.src = downloadUrl(entry.path);
    img.alt = entry.name;
    openPreviewElement(img);
  } else if (hint === 'video') {
    const vid = document.createElement('video');
    vid.controls = true;
    vid.autoplay = false;
    vid.style.maxWidth = '100%';
    const src = document.createElement('source');
    src.src = streamUrlFor(entry);
    vid.appendChild(src);
    openPreviewElement(vid);
  } else if (hint === 'audio') {
    const aud = document.createElement('audio');
    aud.controls = true;
    aud.style.width = '100%';
    const src = document.createElement('source');
    src.src = streamUrlFor(entry);
    aud.appendChild(src);
    openPreviewElement(aud);
  } else if (hint === 'pdf') {
    const embed = document.createElement('embed');
    embed.src = streamUrlFor(entry);
    embed.type = 'application/pdf';
    embed.style.width = '100%';
    embed.style.height = '70vh';
    openPreviewElement(embed);
  } else if (hint === 'text' || hint === 'code') {
    try {
      await previewTextFile(entry, seq);
    } catch (e) {
      toastError(e, 'Cannot preview');
    }
  } else if (hint === 'markdown') {
    try {
      const data = await readFileContent(entry.path);
      if (seq !== _previewSeq || data == null) return;
      const div = document.createElement('div');
      div.className = 'markdown-body';
      div.innerHTML = renderMarkdown(data.content);
      openPreviewElement(div);
    } catch (e) {
      toastError(e, 'Cannot preview');
    }
  } else if (hint === 'archive') {
    if (canExtractArchive(entry)) {
      const extractBtn = document.createElement('button');
      extractBtn.className = 'btn primary';
      extractBtn.style.fontSize = '16px';
      extractBtn.style.padding = '12px 28px';
      extractBtn.innerHTML = ICONS.extract + '<span style="vertical-align:middle;margin-left:8px">Extract archive</span>';
      extractBtn.onclick = () => { closeModal('modal-preview'); openArchiveExtract(entry); };
      const wrap = document.createElement('div');
      wrap.style.display = 'flex';
      wrap.style.alignItems = 'center';
      wrap.style.justifyContent = 'center';
      wrap.style.height = '100%';
      wrap.style.minHeight = '120px';
      wrap.appendChild(extractBtn);
      container.appendChild(wrap);
      openModal('modal-preview');
    } else {
      state.unsupportedEntry = entry;
      document.getElementById('unsupported-filename').textContent = entry.name;
      document.getElementById('unsupported-text').style.display = 'none';
      openModal('modal-unsupported');
    }
  } else {
    // For unrecognized file types, ask the user what to do
    state.unsupportedEntry = entry;
    document.getElementById('unsupported-filename').textContent = entry.name;
    document.getElementById('unsupported-text').style.display = '';
    openModal('modal-unsupported');
  }
}

async function openUnsupportedText() {
  const entry = state.unsupportedEntry;
  if (!entry) return;
  closeModal('modal-unsupported');
  state.unsupportedEntry = null;
  // Open the file as text via the preview modal
  state.previewList = [entry];
  state.previewIndex = 0;
  const container = document.getElementById('preview-container');
  wirePreviewButtons(entry, null);
  container.innerHTML = '';
  try {
    await previewTextFile(entry, ++_previewSeq);
  } catch (e) {
    toastError(e, 'Cannot open as text');
  }
}

function downloadUnsupported() {
  const entry = state.unsupportedEntry;
  if (!entry) return;
  closeModal('modal-unsupported');
  state.unsupportedEntry = null;
  downloadFile(entry.path);
}

// ─── Video Editor ─────────────────────────────────────────────────────────────
function formatTime(seconds) {
  if (seconds == null || isNaN(seconds)) return '—';
  const m = Math.floor(seconds / 60);
  const s = Math.floor(seconds % 60);
  const ms = Math.floor((seconds % 1) * 1000);
  return String(m).padStart(2, '0') + ':' + String(s).padStart(2, '0') + '.' + String(ms).padStart(3, '0');
}

function formatTimeShort(seconds) {
  if (seconds == null || isNaN(seconds)) return '—';
  const m = Math.floor(seconds / 60);
  const s = Math.floor(seconds % 60);
  if (m > 0) return m + 'm ' + s + 's';
  return s + 's';
}

let _videoEditorTimeUpdate = null;
let _thumbnailPollTimer = null;
let _thumbnailPollGeneration = 0;
let _skipCloseConfirm = false;
let _conflictResolve = null;

// _videoEditorSeq fences concurrent openVideoEditor calls: a slow
// /api/video-thumbnails response (or its polling) must not render into the
// timeline of a video opened afterwards (same class of race as _previewSeq).
let _videoEditorSeq = 0;

// Consecutive failed thumbnail polls before polling stops; also caps the
// batch thumbnail queue's contiguous polling failures.
const MAX_THUMBNAIL_POLL_ERRORS = 5;

async function openVideoEditor(entry) {
  const seq = ++_videoEditorSeq;
  state.videoEditorEntry = entry;
  state.videoEditorIntervals = [];
  state.videoEditorStartTime = null;
  state.videoEditorEndTime = null;

  document.getElementById('video-editor-title').textContent = 'Video Editor — ' + entry.name;
  const video = document.getElementById('video-editor-video');
  video.src = streamUrlFor(entry);

  document.getElementById('video-editor-current-time').textContent = '00:00.000';
  document.getElementById('video-editor-start-time').textContent = '—';
  document.getElementById('video-editor-end-time').textContent = '—';
  document.getElementById('video-editor-set-start').disabled = true;
  document.getElementById('video-editor-set-end').disabled = true;
  document.getElementById('video-editor-add-interval').disabled = true;
  renderVideoEditorIntervals();
  document.getElementById('video-editor-timeline').innerHTML = '<div class="video-editor-timeline-loading">Loading thumbnails...</div>';
  document.getElementById('video-editor-reload-video').style.display = 'none';

  openModal('modal-video-editor');
  video.load();

  video.addEventListener('error', function onVideoError() {
    video.removeEventListener('error', onVideoError);
    document.getElementById('video-editor-reload-video').style.display = '';
  });

  // Enable buttons when video metadata is loaded
  video.addEventListener('loadedmetadata', function onMeta() {
    video.removeEventListener('loadedmetadata', onMeta);
    document.getElementById('video-editor-set-start').disabled = false;
    document.getElementById('video-editor-set-end').disabled = false;
  });

  // Update current time display — remove old listener to avoid duplicates
  if (_videoEditorTimeUpdate) {
    video.removeEventListener('timeupdate', _videoEditorTimeUpdate);
  }
  _videoEditorTimeUpdate = () => {
    document.getElementById('video-editor-current-time').textContent = formatTime(video.currentTime);
  };
  video.addEventListener('timeupdate', _videoEditorTimeUpdate);

  // Load thumbnails
  try {
    const data = await apiGet('/api/video-thumbnails?path=' + encodeURIComponent(entry.path));
    if (seq !== _videoEditorSeq) return; // superseded by a newer editor open
    if (!data) return; // session expired; the isUnauthorized redirect is in flight
    renderVideoThumbnails(data.thumbnails || [], data.generating || false, data.total || 0);
    if (data.generating) {
      startThumbnailPolling(entry.path, data.thumbnails || []);
    }
  } catch (e) {
    if (seq !== _videoEditorSeq) return;
    document.getElementById('video-editor-timeline').innerHTML =
      '<div class="video-editor-timeline-loading" style="color:var(--danger)">Failed to load thumbnails: ' + escapeText(e.message) + '</div>';
  }

  // Load previously saved intervals
  if (seq === _videoEditorSeq) loadVideoIntervals(entry.path, seq);
}

function renderVideoThumbnails(thumbs, generating, total) {
  const entry = state.videoEditorEntry;
  if (!entry) return;
  const timeline = document.getElementById('video-editor-timeline');
  timeline.innerHTML = '';
  if (thumbs.length === 0 && !generating) {
    const msg = document.createElement('div');
    msg.className = 'video-editor-timeline-loading';
    msg.textContent = total > 0 ? '0 / ' + total + ' thumbnails found' : 'No thumbnails found';
    timeline.appendChild(msg);
    const btn = document.createElement('button');
    btn.className = 'btn';
    btn.textContent = 'Regenerate';
    btn.style.cssText = 'margin:8px auto 0;font-size:11px;display:block';
    const path = state.videoEditorEntry ? state.videoEditorEntry.path : '';
    btn.onclick = () => {
      apiPost('/api/video-delete-thumbnails', { path }).finally(() => location.reload());
    };
    timeline.appendChild(btn);
    return;
  }

  if (thumbs.length === 0 && generating) {
    const msg = document.createElement('div');
    msg.className = 'video-editor-timeline-loading';
    msg.textContent = 'Generating thumbnails\u2026';
    timeline.appendChild(msg);
    if (total > 0) {
      const prog = document.createElement('div');
      prog.className = 'video-editor-thumb-progress';
      prog.textContent = '0 / ' + total;
      timeline.appendChild(prog);
    }
    return;
  }

  // Scrollable container for thumbnails
  const scrollDiv = document.createElement('div');
  scrollDiv.className = 'video-editor-thumbnails';

  const video = document.getElementById('video-editor-video');

  thumbs.forEach(t => {
    const thumb = document.createElement('div');
    thumb.className = 'video-editor-thumb';
    thumb.title = formatTime(t.time);

    const img = document.createElement('img');
    img.className = 'video-editor-thumb-img';
    img.src = '/api/video-thumbnail-image?path=' + encodeURIComponent(entry.path) + '&id=' + t.index;
    img.alt = formatTime(t.time);
    img.onerror = () => { img.style.display = 'none'; };

    const label = document.createElement('div');
    label.className = 'video-editor-thumb-label';
    label.textContent = t.index + ' ' + formatTimeShort(t.time);

    thumb.appendChild(img);
    thumb.appendChild(label);
    thumb.onclick = () => {
      if (video.readyState >= 1) {
        video.currentTime = t.time;
      } else {
        video.addEventListener('loadedmetadata', function onMeta() {
          video.removeEventListener('loadedmetadata', onMeta);
          video.currentTime = t.time;
        }, { once: true });
      }
    };
    scrollDiv.appendChild(thumb);
  });

  // Progress indicator when still generating
  if (generating) {
    const prog = document.createElement('div');
    prog.className = 'video-editor-thumb-progress';
    if (total > 0) {
      prog.textContent = thumbs.length + ' / ' + total;
    } else {
      prog.textContent = thumbs.length + ' \u2026';
    }
    prog.title = 'Generating thumbnails\u2026';
    scrollDiv.appendChild(prog);
  }

  timeline.appendChild(scrollDiv);

  // Build navigation buttons (every 100th thumbnail)
  const nav = document.getElementById('video-editor-timeline-nav');
  nav.innerHTML = '';
  const step = 100;
  const count = Math.ceil(thumbs.length / step);
  for (let i = 0; i < count; i++) {
    const idx = i * step;
    const btn = document.createElement('button');
    btn.textContent = '#' + (idx + 1);
    btn.title = 'Go to thumbnail ' + (idx + 1);
    btn.onclick = () => {
      const thumb = scrollDiv.children[idx];
      if (thumb) {
        thumb.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'start' });
      }
    };
    nav.appendChild(btn);
  }
  if (thumbs.length > 0) {
    const last = thumbs.length - 1;
    const btn = document.createElement('button');
    btn.textContent = '#' + (last + 1);
    btn.title = 'Go to last thumbnail';
    btn.onclick = () => {
      const thumb = scrollDiv.children[last];
      if (thumb) {
        thumb.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'start' });
      }
    };
    nav.appendChild(btn);
  }
}

function startThumbnailPolling(path, existingThumbs) {
  if (_thumbnailPollTimer) {
    clearTimeout(_thumbnailPollTimer);
  }

  const generation = ++_thumbnailPollGeneration;
  let knownCount = existingThumbs.length;
  let consecutiveErrors = 0;

  async function poll() {
    if (generation !== _thumbnailPollGeneration) return;

    try {
      const data = await apiGet('/api/video-thumbnails?path=' + encodeURIComponent(path));

      if (generation !== _thumbnailPollGeneration) return;

      consecutiveErrors = 0;

      if (data.thumbnails && data.thumbnails.length > knownCount) {
        knownCount = data.thumbnails.length;
        renderVideoThumbnails(data.thumbnails, data.generating || false, data.total || 0);
        // Scroll to the right to show new thumbnails
        const timeline = document.getElementById('video-editor-timeline');
        timeline.scrollLeft = timeline.scrollWidth;
      }
      if (data.generating) {
        _thumbnailPollTimer = setTimeout(poll, 2000);
      } else {
        _thumbnailPollTimer = null;
        // Final render to hide progress indicator
        if (data.thumbnails) {
          renderVideoThumbnails(data.thumbnails, false, data.total || 0);
        }
        // Scroll back to start when generation completes
        const timeline = document.getElementById('video-editor-timeline');
        timeline.scrollLeft = 0;
      }
    } catch (e) {
      // Stop after a run of errors instead of polling forever against a
      // broken server state; the user can reopen the editor to retry.
      consecutiveErrors++;
      if (consecutiveErrors >= MAX_THUMBNAIL_POLL_ERRORS) {
        _thumbnailPollTimer = null;
        return;
      }
      _thumbnailPollTimer = setTimeout(poll, 3000);
    }
  }

  _thumbnailPollTimer = setTimeout(poll, 2000);
}

function renderVideoEditorIntervals() {
  const tbody = document.getElementById('video-editor-table-body');
  const empty = document.getElementById('video-editor-empty');
  const totalEl = document.getElementById('video-editor-total');
  tbody.innerHTML = '';

  if (state.videoEditorIntervals.length === 0) {
    empty.style.display = '';
    totalEl.style.display = 'none';
    return;
  }
  empty.style.display = 'none';

  let totalSec = 0;

  state.videoEditorIntervals.forEach((interval, idx) => {
    const tr = document.createElement('tr');

    const tdIdx = document.createElement('td');
    tdIdx.textContent = idx + 1;
    tr.appendChild(tdIdx);

    const tdStart = document.createElement('td');
    tdStart.textContent = formatTime(interval.start);
    tr.appendChild(tdStart);

    const tdEnd = document.createElement('td');
    tdEnd.textContent = formatTime(interval.end);
    tr.appendChild(tdEnd);

    const dur = interval.end - interval.start;
    totalSec += dur;
    const tdDur = document.createElement('td');
    tdDur.textContent = formatTimeShort(dur);
    tr.appendChild(tdDur);

    const tdDel = document.createElement('td');
    const delBtn = document.createElement('button');
    delBtn.className = 'icon-btn danger';
    delBtn.innerHTML = ICONS.trash;
    delBtn.title = 'Remove interval';
    delBtn.onclick = () => {
      state.videoEditorIntervals.splice(idx, 1);
      renderVideoEditorIntervals();
      saveVideoIntervals();
    };
    tdDel.appendChild(delBtn);
    tr.appendChild(tdDel);

    tbody.appendChild(tr);
  });
  totalEl.style.display = '';
  totalEl.textContent = 'Total: ' + formatTimeShort(totalSec);
}

async function saveVideoIntervals() {
  const entry = state.videoEditorEntry;
  if (!entry) return;
  try {
    await apiPost('/api/video-save-intervals', {
      path: entry.path,
      intervals: state.videoEditorIntervals,
    });
  } catch (e) {
    // Silently ignore — best-effort persistence
  }
}

async function loadVideoIntervals(path, seq) {
  try {
    const data = await apiGet('/api/video-load-intervals?path=' + encodeURIComponent(path));
    // A slow response for video A must not overwrite video B's intervals after
    // the user opened B; drop the result if the editor has moved on.
    if (seq !== undefined && seq !== _videoEditorSeq) return;
    if (data.intervals && data.intervals.length > 0) {
      state.videoEditorIntervals = data.intervals;
      renderVideoEditorIntervals();
    }
  } catch (e) {
    // Silently ignore
  }
}

let _extractOptions = null;

async function doVideoEditorExtract() {
  const entry = state.videoEditorEntry;
  const intervals = state.videoEditorIntervals;

  if (!entry || intervals.length === 0) {
    toast('No intervals to extract', 'error');
    return;
  }

  // Show confirmation modal
  document.getElementById('extract-confirm-clear-tmp').checked = true;
  document.getElementById('extract-confirm-close-editor').checked = true;
  openModal('modal-extract-confirm');
}

document.getElementById('extract-confirm-ok').addEventListener('click', async () => {
  closeModal('modal-extract-confirm');

  const entry = state.videoEditorEntry;
  const intervals = state.videoEditorIntervals;
  if (!entry || intervals.length === 0) return;

  _extractOptions = {
    clearTmp: document.getElementById('extract-confirm-clear-tmp').checked,
    closeEditor: document.getElementById('extract-confirm-close-editor').checked,
  };

  const extractBtn = document.getElementById('video-editor-extract');
  extractBtn.disabled = true;
  extractBtn.textContent = 'Extracting...';

  // Show progress modal
  document.getElementById('extract-progress-title').textContent = 'Extracting segments';
  document.getElementById('extract-progress-status').textContent = 'Starting...';
  document.getElementById('extract-progress-fill').style.width = '0%';
  document.getElementById('extract-progress-percent').textContent = '0%';
  document.getElementById('extract-progress-footer').style.display = '';
  document.getElementById('extract-progress-close').style.display = 'none';
  document.getElementById('extract-progress-command').style.display = 'none';
  document.getElementById('extract-progress-command-text').textContent = '';
  openModal('modal-extract-progress');

  try {
    const response = await fetch('/api/video-extract', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        path: entry.path,
        segments: intervals.map(i => ({ start: i.start, end: i.end })),
      }),
    });

    if (isUnauthorized(response)) return;
    if (!response.ok) throw await readErrorResponse(response);

    await readNdjsonStream(response, handleExtractMessage);
  } catch (e) {
    document.getElementById('extract-progress-status').textContent = 'Error: ' + e.message;
    document.getElementById('extract-progress-status').style.color = 'var(--danger)';
    document.getElementById('extract-progress-close').style.display = '';
    // Reload video to recover from interrupted stream (Safari iOS backgrounding)
    const video = document.getElementById('video-editor-video');
    if (video && video.src) { video.load(); }
  }
});

// Shared success markup for an "Extraction complete" result, used by both the
// archive extraction and video-editor extraction modals. Lives at module scope
// (not inside an NDJSON handler block) so its block binding stays reachable.
function extractionCompleteHtml(subline) {
  return '<div class="extract-result-icon success">&#10003;</div>' +
    '<div>Extraction complete</div>' +
    (subline ? '<div style="font-size:12px;color:var(--text-dim);margin-top:4px">' + subline + '</div>' : '');
}

// showArchiveExtractionComplete renders the shared "Extraction complete" state
// in the archive progress modal and reloads the destination listing. Used by
// both the NDJSON result handler and the connection-loss recovery poll, so the
// two completion paths cannot drift apart.
function showArchiveExtractionComplete(dest, parentDir) {
  const statusEl = document.getElementById('archive-progress-status');
  const fillEl = document.getElementById('archive-progress-fill');
  const percentEl = document.getElementById('archive-progress-percent');
  const titleEl = document.getElementById('archive-progress-title');
  const closeBtn = document.getElementById('archive-progress-close');
  statusEl.innerHTML = extractionCompleteHtml(escapeText(dest));
  fillEl.className = '';
  fillEl.style.width = '100%';
  fillEl.style.background = 'var(--success)';
  percentEl.textContent = 'Done';
  titleEl.textContent = 'Extraction complete';
  closeBtn.style.display = '';
  // loadDirectory prompts with a navigation-confirm modal whenever something is
  // selected; doing that from inside an open progress modal would stack a
  // second modal on top. Extraction success discards the selection explicitly.
  state.selectedFiles.clear();
  updateSelectionButtons();
  loadDirectory(parentDir);
}

function handleExtractMessage(msg) {
  const statusEl = document.getElementById('extract-progress-status');
  const fillEl = document.getElementById('extract-progress-fill');
  const percentEl = document.getElementById('extract-progress-percent');

  const cmdWrap = document.getElementById('extract-progress-commands-wrap');
  const cmdToggle = document.getElementById('extract-progress-commands-toggle');
  const cmdEl = document.getElementById('extract-progress-command');
  const cmdText = document.getElementById('extract-progress-command-text');

  if (msg.type === 'progress') {
    if (msg.command) {
      cmdWrap.style.display = '';
      // Append text nodes instead of textContent +=, which re-serialises the
      // whole buffer on every command (O(n²) on a long extraction log).
      cmdText.appendChild(document.createTextNode(
        (cmdText.childNodes.length > 0 ? '\n\n' : '') + msg.command));
    }
    if (msg.status === 'concatenating') {
      statusEl.textContent = 'Concatenating segments\u2026';
      fillEl.style.width = '100%';
      percentEl.textContent = '100%';
    } else if (msg.segment != null && msg.total > 0) {
      const pct = Math.round((msg.segment / msg.total) * 100);
      statusEl.textContent = 'Extracting segment ' + msg.segment + ' / ' + msg.total;
      fillEl.style.width = pct + '%';
      percentEl.textContent = pct + '%';
    }
  } else if (msg.type === 'result' && msg.status === 'ok') {
    statusEl.innerHTML = extractionCompleteHtml() +
      '<div id="extract-result-output">' + escapeText(basename(msg.output)) + '</div>';
    statusEl.style.color = '';
    fillEl.style.width = '100%';
    percentEl.textContent = 'Done';
    document.getElementById('extract-progress-title').textContent = 'Extraction complete';
    document.getElementById('extract-progress-close').style.display = '';

    // Handle post-extraction options
    if (_extractOptions) {
      if (_extractOptions.clearTmp && state.videoEditorEntry) {
        apiPost('/api/video-delete-thumbnails', { path: state.videoEditorEntry.path });
      }
      if (_extractOptions.closeEditor) {
        _skipCloseConfirm = true;
        closeModal('modal-video-editor');
      }
    }

    // Refresh the directory where the output was placed. Clear any selection
    // first so loadDirectory does not interrupt the progress modal with a
    // navigation-confirm overlay.
    state.selectedFiles.clear();
    updateSelectionButtons();
    loadDirectory(parentPath(msg.output));
  } else if (msg.type === 'error') {
    statusEl.textContent = 'Error: ' + msg.message;
    statusEl.style.color = 'var(--danger)';
    document.getElementById('extract-progress-title').textContent = 'Extraction failed';
    document.getElementById('extract-progress-close').style.display = '';
  }
}

async function cancelVideoThumbnails() {
  const entry = state.videoEditorEntry;
  if (!entry) return;
  try {
    await apiPost('/api/video-cancel-thumbnails', { path: entry.path });
  } catch (e) {
    // Silently ignore — best-effort cancellation
  }
}

// ─── Batch Thumbnails ─────────────────────────────────────────────────────────
function showThumbProgress(label, fileInfo, pct, sub) {
  const el = document.getElementById('thumb-progress');
  if (!el) return;
  el.style.display = 'block';
  document.getElementById('thumb-progress-label').textContent = label;
  if (fileInfo != null) {
    document.getElementById('thumb-progress-file').textContent = fileInfo;
  }
  setProgressFill(document.getElementById('thumb-progress-fill'), pct);
  document.getElementById('thumb-progress-sub').textContent = sub || '';
}

function showThumbnailProgress(completed, total) {
  const wrap = document.getElementById('thumb-progress-thumb-wrap');
  const label = document.getElementById('thumb-progress-thumb-label');
  const fill = document.getElementById('thumb-progress-thumb-fill');
  if (!wrap || !label || !fill) return;
  if (total > 0) {
    wrap.style.display = '';
    label.textContent = 'Thumbnails: ' + completed + ' / ' + total;
    fill.style.width = Math.round((completed / total) * 100) + '%';
  } else {
    wrap.style.display = 'none';
  }
}

function hideThumbProgress() {
  const el = document.getElementById('thumb-progress');
  if (el) el.style.display = 'none';
  state.batchThumbCancel = null;
}

async function startBatchThumbnails() {
  const paths = [...state.selectedFiles];
  if (paths.length < 2) {
    toast('Select at least 2 video files', 'error');
    return;
  }

  if (!confirm(`Generate thumbnails for ${paths.length} video file${paths.length > 1 ? 's' : ''}? This may take a while.`)) return;

  // Start all background generations (non-streaming, works in Safari background tabs)
  let active = paths;
  try {
    const res = await apiPost('/api/video-batch-thumbnails', { paths });
    // res.skipped maps paths the server rejected (with a reason) before
    // queueing; poll only the jobs the server actually accepted, and mark the
    // rest failed immediately instead of letting the poll loop run them
    // forever and eventually trip its poll-error limit.
    const skipped = res.skipped || {};
    active = (res.paths || paths).filter(p => !(p in skipped));
    for (const [p, reason] of Object.entries(skipped)) {
      toast(basename(p) + ': skipped - ' + reason, 'error');
    }
    if (active.length === 0) return;
  } catch (e) {
    toastError(e, 'Failed to start');
    return;
  }

  startBatchPolling(active, /* clearSelection */ true);
}

function startBatchPolling(paths, clearSelection) {
  const fileStatus = new Map();
  paths.forEach(p => fileStatus.set(p, { status: 'pending', total: 0, completed: 0, error: null }));

  let cancelled = false;
  state.batchThumbCancel = () => {
    cancelled = true;
    paths.forEach(p => {
      apiPost('/api/video-cancel-thumbnails', { path: p }).catch(() => {});
    });
  };

  showThumbProgress('Creating Thumbnails', 'Starting...', 0, '0 / ' + paths.length + ' files');

  const POLL_MS = 2000;

  // Stop rather than poll forever when a persistent failure (not an in-flight
  // batch) keeps failing; an endless silent 2s retry loop helps nobody.
  const MAX_CONSECUTIVE_POLL_ERRORS = MAX_THUMBNAIL_POLL_ERRORS;
  let consecutiveErrors = 0;

  async function poll() {
    if (cancelled) {
      cleanupBatch();
      return;
    }

    let allDone = true;
    let errorCount = 0;
    let doneCount = 0;

    for (let i = 0; i < paths.length; i++) {
      if (cancelled) { allDone = true; break; }

      const p = paths[i];
      const st = fileStatus.get(p);
      if (st.status === 'done' || st.status === 'error') {
        if (st.status === 'error') errorCount++;
        doneCount++;
        continue;
      }

      try {
        if (cancelled) { allDone = true; break; }

        const data = await apiGet('/api/video-thumbnails?path=' + encodeURIComponent(p));

        if (cancelled) { allDone = true; break; }

        consecutiveErrors = 0;

        if (data.error) {
          st.status = 'error';
          st.error = data.error;
          toast(basename(p) + ': ' + data.error, 'error');
          doneCount++;
          continue;
        }

        if (data.waiting) {
          allDone = false;
          continue;
        }

        if (!data.generating) {
          st.status = 'done';
          st.completed = (data.thumbnails || []).length;
          st.total = st.completed;
          doneCount++;
        } else {
          allDone = false;
          st.status = 'generating';
          st.total = data.total || 0;
          st.completed = data.completed || 0;
        }
      } catch (e) {
        consecutiveErrors++;
        if (consecutiveErrors >= MAX_CONSECUTIVE_POLL_ERRORS) {
          toast('Thumbnail creation stopped: ' + (e && e.message ? e.message : 'status checks keep failing'), 'error');
          cleanupBatch();
          return;
        }
        allDone = false;
      }
    }

    if (cancelled) { cleanupBatch(); return; }

    // Find the current generating file for display
    const currentEntry = [...fileStatus.entries()].find(([_, s]) => s.status === 'generating');
    const currentFileName = currentEntry ? basename(currentEntry[0]) : '';
    const currentIdx = currentEntry ? paths.indexOf(currentEntry[0]) + 1 : doneCount;
    const currentStatus = currentEntry ? currentEntry[1] : null;

    const fileInfo = currentFileName
      ? 'File ' + currentIdx + '/' + paths.length + ': ' + currentFileName
      : doneCount + '/' + paths.length + ' files done';

    const overallPct = Math.round((doneCount / paths.length) * 100);

    showThumbProgress('Creating Thumbnails', fileInfo, overallPct,
      currentStatus ? 'File ' + currentIdx + ' of ' + paths.length : '');

    if (currentStatus) {
      showThumbnailProgress(currentStatus.completed, currentStatus.total);
    } else if (doneCount < paths.length) {
      const thumbWrap = document.getElementById('thumb-progress-thumb-wrap');
      if (thumbWrap) thumbWrap.style.display = 'none';
    }

    if (allDone) {
      toast(errorCount > 0 ? 'Done with ' + errorCount + ' error(s)' : 'Thumbnail creation complete',
        errorCount > 0 ? 'error' : 'success');
      cleanupBatch();
      return;
    }

    setTimeout(poll, POLL_MS);
  }

  function cleanupBatch() {
    hideThumbProgress();
    state.batchThumbCancel = null;
    if (clearSelection) {
      state.selectedFiles.clear();
      updateSelectionButtons();
      loadDirectory(state.currentPath);
    }
  }

  setTimeout(poll, 500);
}

async function resumeBatchIfActive() {
  try {
    const data = await apiGet('/api/video-batch-status');
    if (data.active && data.paths && data.paths.length > 0) {
      startBatchPolling(data.paths, /* clearSelection */ false);
    }
  } catch (e) {
    // Ignore errors — batch status is purely a recovery mechanism
  }
}

// ─── Slideshow ────────────────────────────────────────────────────────────────
function getImageEntries() {
  return getFilteredEntries().filter(e => !e.is_dir && e.mime_hint === 'image');
}

function showSlideshowImage() {
  const entry = state.slideshowImages[state.slideshowIndex];
  if (!entry) { stopSlideshow(); return; }
  const img = document.getElementById('slideshow-img');
  img.src = '/api/download?path=' + encodeURIComponent(entry.path);
  img.alt = entry.name;
  state.slideshowZoom = 1;
  applySlideshowZoom();
  updateSlideshowControls();
}

function scheduleSlideshowNext() {
  if (state.slideshowTimer) { clearTimeout(state.slideshowTimer); }
  state.slideshowTimer = setTimeout(goSlideshowNext, state.slideshowInterval);
}

function goSlideshowStep(dir) {
  const n = state.slideshowImages.length;
  if (n === 0) { stopSlideshow(); return; }
  state.slideshowIndex = (state.slideshowIndex + dir + n) % n;
  showSlideshowImage();
  if (!state.slideshowPaused) scheduleSlideshowNext();
}

function goSlideshowNext() { goSlideshowStep(1); }

function goSlideshowPrev() { goSlideshowStep(-1); }

function updateSlideshowControls() {
  const total = state.slideshowImages.length;
  const idx = state.slideshowIndex + 1;
  document.getElementById('slideshow-counter').textContent = total > 0 ? idx + '/' + total : '';
  document.getElementById('slideshow-status').textContent = state.slideshowPaused ? '⏸' : '▶';
  document.getElementById('slideshow-speed-val').textContent = (state.slideshowInterval / 1000).toFixed(1) + 's';
  document.getElementById('slideshow-zoom-val').textContent = Math.round(state.slideshowZoom * 100) + '%';
}

function startSlideshow() {
  const images = getImageEntries();
  if (images.length === 0) { toast('No images in this folder', 'error'); return; }

  state.slideshowImages = images;
  state.slideshowIndex = 0;
  state.slideshowPaused = false;
  state.slideshowActive = true;
  state.slideshowZoom = 1;

  const overlay = document.getElementById('slideshow-overlay');
  overlay.classList.add('active');

  const el = document.documentElement;
  if (el.requestFullscreen) { el.requestFullscreen(); }
  else if (el.webkitRequestFullscreen) { el.webkitRequestFullscreen(); }

  showSlideshowImage();
  scheduleSlideshowNext();
  updateSlideshowControls();
  showSlideshowControls();
}

function stopSlideshow() {
  state.slideshowActive = false;
  state.slideshowZoom = 1;
  if (state.slideshowTimer) { clearTimeout(state.slideshowTimer); state.slideshowTimer = null; }
  // A pending auto-hide of the on-screen controls must not fire after the
  // slideshow has already stopped.
  if (state.slideshowControlsTimer) { clearTimeout(state.slideshowControlsTimer); state.slideshowControlsTimer = null; }

  document.getElementById('slideshow-overlay').classList.remove('active');
  document.getElementById('slideshow-container').classList.remove('zoomed');

  if (document.fullscreenElement) { document.exitFullscreen(); }
  else if (document.webkitFullscreenElement) { document.webkitExitFullscreen(); }
}

function toggleSlideshowPause() {
  if (!document.getElementById('slideshow-overlay').classList.contains('active')) return;
  state.slideshowResumeAfterZoom = false;
  state.slideshowPaused = !state.slideshowPaused;
  if (state.slideshowPaused) {
    if (state.slideshowTimer) { clearTimeout(state.slideshowTimer); state.slideshowTimer = null; }
  } else {
    scheduleSlideshowNext();
  }
  updateSlideshowControls();
}

function showSlideshowControls() {
  const ctrl = document.getElementById('slideshow-controls');
  ctrl.classList.add('visible');
  if (state.slideshowControlsTimer) { clearTimeout(state.slideshowControlsTimer); }
  state.slideshowControlsTimer = setTimeout(() => {
    ctrl.classList.remove('visible');
    state.slideshowControlsTimer = null;
  }, 2500);
}

function adjustSlideshowSpeed(delta) {
  let newInterval = state.slideshowInterval + delta;
  if (newInterval < 500) newInterval = 500;
  if (newInterval > 10000) newInterval = 10000;
  state.slideshowInterval = newInterval;
  updateSlideshowControls();
  if (!state.slideshowPaused) {
    if (state.slideshowTimer) { clearTimeout(state.slideshowTimer); }
    scheduleSlideshowNext();
  }
}

function applySlideshowZoom() {
  const img = document.getElementById('slideshow-img');
  const container = document.getElementById('slideshow-container');
  const zoom = state.slideshowZoom;
  if (zoom === 1) {
    img.style.width = '';
    img.style.height = '';
    img.style.objectFit = '';
    container.classList.remove('zoomed');
  } else {
    img.style.objectFit = 'none';
    if (img.naturalWidth && img.naturalHeight) {
      img.style.width = (img.naturalWidth * zoom) + 'px';
      img.style.height = (img.naturalHeight * zoom) + 'px';
    }
    container.classList.add('zoomed');
  }
  updateSlideshowControls();
}

function slideshowZoomIn() {
  state.slideshowZoom = Math.min(state.slideshowZoom * 1.5, 5);
  applySlideshowZoom();
  if (!state.slideshowPaused) { toggleSlideshowPause(); state.slideshowResumeAfterZoom = true; }
}

function slideshowZoomOut() {
  state.slideshowZoom = Math.max(state.slideshowZoom / 1.5, 0.25);
  applySlideshowZoom();
  if (!state.slideshowPaused) { toggleSlideshowPause(); state.slideshowResumeAfterZoom = true; }
}

function slideshowZoomReset() {
  state.slideshowZoom = 1;
  applySlideshowZoom();
  if (state.slideshowPaused && state.slideshowResumeAfterZoom) toggleSlideshowPause();
}

// ─── Disk info ────────────────────────────────────────────────────────────────
async function loadDiskInfo() {
  // Fence against nav races like loadDirectory's _loadSeq: navigate while the
  // request is in flight and a stale response could paint another folder's
  // disk numbers onto the widget of the current path.
  const path = state.currentPath;
  try {
    const params = new URLSearchParams({ path: state.currentPath });
    const data = await apiGet('/api/info?' + params);
    if (path !== state.currentPath) return; // superseded by a newer navigation
    // The disk info endpoint reports errors via data.disk_error (e.g. a base
    // directory on a vanished mount); without this a stale widget keeps
    // showing the previous mount's numbers for every future path.
    const disk = data.disk;
    // The disk error branch is only reachable when it is the actual ID: the
    // sidebar widget is id="disk-info" (index.html).
    const el = document.getElementById('disk-info');
    if (!disk || data.disk_error) {
      if (el) el.style.display = 'none';
      return;
    }
    if (el) el.style.display = '';
    const pct = Math.round(disk.used_pct || 0);
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
    state.favourites = (await apiGet('/api/favourites')) || [];
    const list = document.getElementById('fav-list');
    list.innerHTML = '';
    const nameCounts = {};
    state.favourites.forEach(fav => { nameCounts[fav.name] = (nameCounts[fav.name] || 0) + 1; });
    state.favourites.forEach((fav, idx) => {
      const el = document.createElement('div');
      el.className = 'fav-item';
      el.dataset.path = fav.path;

      // Icon (direct child of fav-item for proper gap spacing)
      const iconEl = document.createElement('span');
      iconEl.innerHTML = ICONS.dir; // safe: ICONS.dir is a static constant

      // Name (without icon); show the path too when the name is duplicated
      const nameWrap = document.createElement('span');
      nameWrap.className = 'fav-name';
      const nameSpan = document.createElement('span');
      nameSpan.className = 'fav-name-text';
      nameSpan.textContent = fav.name;
      nameWrap.appendChild(nameSpan);
      if (nameCounts[fav.name] > 1) {
        const pathSpan = document.createElement('span');
        pathSpan.className = 'fav-name-path';
        pathSpan.textContent = fav.path;
        pathSpan.title = fav.path;
        nameWrap.appendChild(pathSpan);
      }

      const controls = document.createElement('span');
      controls.className = 'fav-controls';

      const editBtn = document.createElement('button');
      editBtn.className = 'fav-remove-btn';
      editBtn.title = 'Edit favourite name';
      editBtn.textContent = '✎';
      editBtn.onclick = (e) => {
        e.stopPropagation();
        editFavourite(idx);
      };
      controls.appendChild(editBtn);

      // Remove button (hidden until hover)
      const removeBtn = document.createElement('button');
      removeBtn.className = 'fav-remove-btn';
      removeBtn.title = 'Remove from Favourites';
      removeBtn.textContent = '✕';
      removeBtn.disabled = fav.path === '/';
      removeBtn.onclick = async (e) => {
        e.stopPropagation();
        try {
          await deleteFavourite(idx);
        } catch (err) {
          toastError(err);
        }
      };
      controls.appendChild(removeBtn);

      el.appendChild(iconEl);
      el.appendChild(nameWrap);
      el.appendChild(controls);
      el.onclick = () => loadDirectory(fav.path);
      list.appendChild(el);
    });
    updateActiveFav();
  } catch (_) { /* non-critical */ }
}

function isFavourite(path) {
  return state.favourites.some(f => f.path === path);
}

async function saveFavourites(favourites) {
  await apiPost('/api/favourites/update', { favourites });
  await loadFavourites();
  renderFileList();
}

async function editFavourite(index) {
  const fav = state.favourites[index];
  if (!fav) return;
  const next = window.prompt('Favourite name', fav.name);
  if (next == null) return;
  const name = next.trim();
  if (!name) return toast('Favourite name cannot be empty', 'error');
  const updated = [...state.favourites];
  updated[index] = { ...fav, name };
  try {
    await saveFavourites(updated);
  } catch (err) {
    toastError(err);
  }
}

function confirmFavouriteRemoval(fav) {
  if (!fav) return false;
  return window.confirm(`Remove favourite "${fav.name}"?`);
}

async function deleteFavourite(index) {
  const fav = state.favourites[index];
  if (!fav || fav.path === '/') return;
  if (!confirmFavouriteRemoval(fav)) return;
  const updated = state.favourites.filter((_, i) => i !== index);
  await saveFavourites(updated);
}

async function addFavouriteFromCurrentPath() {
  const pathInput = window.prompt('Favourite path', state.currentPath || '/');
  if (pathInput == null) return;
  const favPath = pathInput.trim() || '/';
  const defaultName = favPath === '/' ? 'Home' : basename(favPath);
  const nameInput = window.prompt('Favourite name', defaultName);
  if (nameInput == null) return;
  const favName = nameInput.trim() || defaultName;
  try {
    await apiPost('/api/favourites/add', { path: favPath, name: favName });
    await loadFavourites();
    renderFileList();
  } catch (err) {
    toastError(err);
  }
}

async function toggleFavourite(path, name, btn) {
  try {
    if (isFavourite(path)) {
      // Unstar must not gate on a window.confirm: the star click is a plain
      // toggle, and a confirm here makes a suppressed or dismissed dialog look
      // like "the button never turns off" until a reload. Removing a favourite
      // is trivially reversible, so no confirmation is warranted.
      await apiPost('/api/favourites/remove', { path });
    } else {
      await apiPost('/api/favourites/add', { path, name });
    }
    await loadFavourites();
    if (btn) {
      const isNowFav = isFavourite(path);
      btn.innerHTML = isNowFav ? ICONS.starFilled : ICONS.starEmpty;
      btn.classList.toggle('fav-active', isNowFav);
      btn.title = isNowFav ? 'Remove from Favourites' : 'Add to Favourites';
    }
  } catch (err) {
    toastError(err);
  }
}

// ─── Protected Paths ─────────────────────────────────────────────────────────
async function loadProtectedPaths() {
  try {
    state.protectedPaths = (await apiGet('/api/protected')) || [];
  } catch (_) {}
}

function isProtected(path) {
  return state.protectedPaths.some(p => {
    const normalized = p.replace(/\/+$/, '') || '/';
    return path === normalized || (normalized !== '/' && path.startsWith(normalized + '/'));
  });
}

async function toggleProtection(entry, btn) {
  try {
    const path = entry.path;
    const currentlyProtected = state.protectedPaths.includes(path);
    if (currentlyProtected) {
      await apiPost('/api/protected/remove', { path });
    } else {
      await apiPost('/api/protected/add', { path });
    }
    await loadProtectedPaths();
    updateProtectBtn(btn, path);
    toast(currentlyProtected ? 'Delete protection removed' : 'Delete protection enabled', 'success');
  } catch (e) {
    toastError(e, 'Failed to update protection');
  }
}

// Shared markup for the per-row delete-protection control: a toggle button when
// the entry can be protected, otherwise an inert badge for a parent-protected
// entry. Used by both the file-list renderer and the button state updater so the
// icon/title/class decisions stay in one place.
function protectButtonHtml(path) {
  const dp = escapeAttr(path);
  const direct = state.protectedPaths.includes(path);
  const inherited = !direct && isProtected(path);
  if (inherited) {
    return `<span class="icon-btn inherited-protected" title="Protected by parent folder">${ICONS.lockInherited}</span>`;
  }
  return `<button class="icon-btn${direct ? ' protected-active' : ''}" title="${direct ? 'Remove delete protection' : 'Protect from deletion'}" data-action="protect" data-path="${dp}">${direct ? ICONS.lockClosed : ICONS.lockOpen}</button>`;
}

function updateProtectBtn(btn, path) {
  btn.outerHTML = protectButtonHtml(path);
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

function openModalFocus(modalId, focusId) {
  openModal(modalId);
  setTimeout(() => {
    const el = document.getElementById(focusId);
    if (el) el.focus();
  }, 50);
}

function closeModal(id, manual = true) {
  // Progress modals hide their close button for the whole run; closing them
  // early (e.g. via the global Escape handler) would leave the UI mid-operation
  // while the server is still working. The archive modal guards against
  // collapsing the progress overlay; the extract modal guards against
  // re-enabling the Extract button while a duplicate run is still streaming.
  const guardedClose = [
    ['modal-archive-progress', 'archive-progress-close'],
    ['modal-extract-progress', 'extract-progress-close'],
  ];
  for (const [modalId, closeBtnId] of guardedClose) {
    if (id === modalId) {
      const closeBtn = document.getElementById(closeBtnId);
      if (closeBtn && closeBtn.style.display === 'none') return;
    }
  }
  const el = document.getElementById(id);
  if (!el) return;
  // A conflict dialog closed by Escape/✕/side-effects must still fulfil the
  // promise the move/copy is awaiting, otherwise that operation hangs forever.
  if (id === 'modal-conflict' && _conflictResolve) {
    const resolve = _conflictResolve;
    _conflictResolve = null;
    resolve({ action: 'cancel', applyToAll: false });
  }
  // Confirm before hiding the video editor while thumbnails are still
  // generating. This must run before classList.remove('open') so that
  // cancelling keeps the modal open and the video playing.
  if (id === 'modal-video-editor' && _thumbnailPollTimer && !_skipCloseConfirm) {
    if (!confirm('Thumbnails are still generating. Cancel and close?')) {
      return;
    }
  }
  if (id === 'modal-navigate' && _navigateModalResolve) {
    const resolve = _navigateModalResolve;
    _navigateModalResolve = null;
    resolve(false);
  }
  if (id === 'modal-delete') {
    if (_deleteModalResolve) {
      const resolve = _deleteModalResolve;
      state.deletePaths = null;
      _deleteModalResolve = null;
      resolve(false);
    }
    const ew = document.getElementById('delete-extra-warning');
    if (ew) ew.style.display = 'none';
    const checkEl = document.getElementById('delete-confirm-check');
    if (checkEl) checkEl.checked = false;
    // Re-apply the persisted secure-delete preference so the next open
    // reflects the user's last choice rather than a forced reset.
    applyDeletePrefs();
    const deleteBtn = document.getElementById('delete-confirm');
    if (deleteBtn) deleteBtn.disabled = false;
  }
  if (id === 'modal-unsupported') {
    state.unsupportedEntry = null;
  }
  // A text/markdown read may still be in flight when the user closes the
  // preview; bumping the sequence here makes openPreviewElement() bail instead
  // of popping a modal the user just dismissed (same class of race as the
  // nav fence, but for the preview slot).
  if (id === 'modal-preview') {
    _previewSeq++;
  }
  el.classList.remove('open');
  // Stop any media playing inside the modal
  el.querySelectorAll('video, audio').forEach(m => { m.pause(); m.src = ''; });
  // Stop thumbnail polling and clean up video editor when video editor closes
  if (id === 'modal-video-editor') {
    // Only delete the cached thumbnails when the user accepted the cancellation
    // prompt (generating && !skip). The extraction-complete path (_skipCloseConfirm)
    // intentionally preserves them for the resumed editor.
    if (_thumbnailPollTimer && !_skipCloseConfirm) {
      const entry = state.videoEditorEntry;
      if (entry) {
        apiPost('/api/video-delete-thumbnails', { path: entry.path });
      }
    }
    _skipCloseConfirm = false;
    if (_thumbnailPollTimer) {
      clearTimeout(_thumbnailPollTimer);
      _thumbnailPollTimer = null;
    }
    // Invalidate any poll() that already checked its generation guard but is
    // still awaiting a response: without this, a poll in flight when the
    // editor closed would re-arm _thumbnailPollTimer and keep polling the
    // closed video's hidden DOM forever.
    _thumbnailPollGeneration++;
    if (_videoEditorTimeUpdate) {
      const video = document.getElementById('video-editor-video');
      video.removeEventListener('timeupdate', _videoEditorTimeUpdate);
      _videoEditorTimeUpdate = null;
    }
    // Cancel any running thumbnail generation (single call — was duplicated)
    cancelVideoThumbnails();
  }
  if (id === 'modal-archive-extract') {
    state._archiveExtractEntry = null;
  }
  if (id === 'modal-archive-progress') {
    const outputPre = document.getElementById('archive-progress-output');
    if (outputPre) outputPre.textContent = '';
  }
  if (id === 'modal-extract-progress') {
    const extractBtn = document.getElementById('video-editor-extract');
    extractBtn.disabled = false;
    extractBtn.textContent = 'Extract';
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
  // dragend is the reliable escape hatch when the drag is cancelled over
  // non-DOM chrome (taskbar, browser UI) and dragleave never fires; without
  // it the overlay can stick around permanently.
  document.addEventListener('dragend', () => {
    dragCounter = 0;
    overlay.classList.remove('visible');
  });
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

// Breakpoint (px) below which the mobile sidebar overlay is used instead of
// the desktop collapse behaviour.  Must match the CSS @media threshold.
const MOBILE_BREAKPOINT = 700;

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

  if (document.getElementById('slideshow-overlay').classList.contains('active')) {
    if (e.key === 'Escape') { e.preventDefault(); stopSlideshow(); return; }
    if (e.key === 'ArrowLeft') { e.preventDefault(); goSlideshowPrev(); return; }
    if (e.key === 'ArrowRight') { e.preventDefault(); goSlideshowNext(); return; }
    if (e.key === ' ') { e.preventDefault(); toggleSlideshowPause(); return; }
    if (e.key === '+' || e.key === '=') { e.preventDefault(); slideshowZoomIn(); return; }
    if (e.key === '-' || e.key === '_') { e.preventDefault(); slideshowZoomOut(); return; }
    if (e.key === '0') { e.preventDefault(); slideshowZoomReset(); return; }
  }

  if (e.key === 'F5') { e.preventDefault(); loadDirectory(state.currentPath); }
  if (e.key === 'Delete') {
    // Key auto-repeat from a held key must not re-fire the flow, and the
    // confirmation modal already owns the decision (the key handler would
    // otherwise stack a second promise behind the first one).
    if (e.repeat || document.getElementById('modal-delete').classList.contains('open')) return;
    // Skip protected paths: the Delete key is a blunt instrument and must not
    // bypass the protection that guards the interactive buttons.
    const toDelete = [...state.selectedFiles].filter(p => !isProtected(p));
    if (toDelete.length > 0) deleteFiles(toDelete);
  }
  if (e.key === 'Escape') {
    document.querySelectorAll('.modal-backdrop.open').forEach(m => {
      // The bulk-operation progress modal must stay visible while the op is
      // running — its only purpose is to show a cancel button for that op.
      if (m.id === 'modal-op-progress' && state.opAbort) return;
      closeModal(m.id);
    });
  }
});

// ─── Archive Extraction ────────────────────────────────────────────────────────
function canExtractArchive(entry) {
  if (entry.mime_hint !== 'archive') return false;
  const nameLower = entry.name.toLowerCase();
  const ext = nameLower.split('.').pop();
  const tarExts = ['tar', 'tgz', 'tbz2'];
  return (
    ((ext === 'zip' || ext === 'rar') && state.sevenzipAvailable) ||
    (ext === 'zip' && state.unzipAvailable) ||
    (ext === 'rar' && state.unrarAvailable) ||
    (ext === '7z' && state.sevenzipAvailable) ||
    ((tarExts.includes(ext) ||
      nameLower.endsWith('.tar.gz') ||
      nameLower.endsWith('.tar.bz2') ||
      nameLower.endsWith('.tar.xz')) && state.tarAvailable)
  );
}

function openArchiveExtract(entry) {
  document.getElementById('archive-extract-filename').textContent = entry.name;
  document.getElementById('archive-extract-password').value = '';
  state._archiveExtractEntry = entry;
  openModalFocus('modal-archive-extract', 'archive-extract-password');
}

async function doArchiveExtract() {
  const entry = state._archiveExtractEntry;
  if (!entry) return;
  state._archiveExtractEntry = null;

  const password = document.getElementById('archive-extract-password').value;
  closeModal('modal-archive-extract');

  const titleEl = document.getElementById('archive-progress-title');
  const statusEl = document.getElementById('archive-progress-status');
  const fillEl = document.getElementById('archive-progress-fill');
  const percentEl = document.getElementById('archive-progress-percent');
  const outputWrap = document.getElementById('archive-progress-output-wrap');
  const outputPre = document.getElementById('archive-progress-output');
  const footerEl = document.getElementById('archive-progress-footer');
  const closeBtn = document.getElementById('archive-progress-close');

  // Compute expected destination for recovery after connection loss
  const archiveParent = parentPath(entry.path);
  const stem = archiveStem(entry.name);
  const expectedDest = (archiveParent === '/' ? '' : archiveParent) + '/' + stem;

  titleEl.textContent = 'Extracting ' + entry.name;
  statusEl.textContent = 'Starting extraction...';
  statusEl.style.color = '';
  fillEl.className = 'indeterminate';
  fillEl.style.width = '100%';
  percentEl.textContent = 'Extracting...';
  outputWrap.style.display = 'none';
  outputPre.textContent = '';
  outputPre.style.display = 'none';
  footerEl.style.display = '';
  closeBtn.style.display = 'none';

  openModal('modal-archive-progress');

  try {
    const response = await fetch('/api/extract-archive', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: entry.path, password: password || '' }),
    });

    if (isUnauthorized(response)) return;
    if (!response.ok) throw await readErrorResponse(response);

    await readNdjsonStream(response, handleArchiveExtractMessage);
  } catch (e) {
    const isNetworkErr = e instanceof TypeError ||
      /network(?:Error)?|fetch|load(?:ing)?|interrupt|abort/i.test(e.message);
    if (isNetworkErr) {
      statusEl.innerHTML =
        '<div style="font-size:32px;color:var(--warning);line-height:1;margin-bottom:6px">&#9888;</div>' +
        '<div>Connection lost</div>' +
        '<div style="font-size:12px;color:var(--text-dim);margin-top:4px">Checking for completion...</div>';
      statusEl.style.color = '';
      percentEl.textContent = 'Reconnecting';
      fillEl.className = 'indeterminate';
      fillEl.style.width = '100%';

      // Poll to detect when server-side extraction completes
      let pollTimer = null;
      let stopped = false;
      const stopPoll = () => { stopped = true; if (pollTimer) clearTimeout(pollTimer); };
      const closeHandler = () => { stopPoll(); closeBtn.removeEventListener('click', closeHandler); };
      closeBtn.addEventListener('click', closeHandler);

      const poll = async () => {
        if (stopped) return;
        try {
          const entries = await apiList(archiveParent, false);
          if (stopped) return;
          if (entries && entries.some(e => e.name === stem && e.is_dir)) {
            showArchiveExtractionComplete(expectedDest, archiveParent);
            stopPoll();
            return;
          }
        } catch (_) {
          if (stopped) return;
        }
        pollTimer = setTimeout(poll, 2000);
      };
      pollTimer = setTimeout(poll, 3000);
    } else {
      statusEl.textContent = 'Error: ' + e.message;
      statusEl.style.color = 'var(--danger)';
      percentEl.textContent = 'Failed';
      fillEl.className = '';
      fillEl.style.width = '0%';
      closeBtn.style.display = '';
    }
  }
}

function handleArchiveExtractMessage(msg) {
  const statusEl = document.getElementById('archive-progress-status');
  const fillEl = document.getElementById('archive-progress-fill');
  const percentEl = document.getElementById('archive-progress-percent');
  const outputWrap = document.getElementById('archive-progress-output-wrap');
  const outputPre = document.getElementById('archive-progress-output');
  const closeBtn = document.getElementById('archive-progress-close');
  const titleEl = document.getElementById('archive-progress-title');

  if (msg.type === 'progress') {
    if (msg.line) {
      outputWrap.style.display = '';
      outputPre.appendChild(document.createTextNode(msg.line + '\n'));
      outputPre.scrollTop = outputPre.scrollHeight;
    }
    if (msg.line && !msg.line.startsWith('replace')) {
      statusEl.textContent = msg.line;
    }
    fillEl.className = '';
    fillEl.style.width = '';
    fillEl.classList.add('indeterminate');
    percentEl.textContent = 'Extracting...';
  } else if (msg.type === 'result' && msg.status === 'ok') {
    showArchiveExtractionComplete(msg.dest, parentPath(msg.dest));
  } else if (msg.type === 'error') {
    statusEl.textContent = 'Error: ' + msg.message;
    statusEl.style.color = 'var(--danger)';
    fillEl.className = '';
    fillEl.style.width = '0%';
    percentEl.textContent = 'Failed';
    titleEl.textContent = 'Extraction failed';
    closeBtn.style.display = '';
  }
}

// ─── Init ─────────────────────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', () => {
  // Apply saved font size immediately
  loadFontSize();

  // Read initial path from URL
  const url = new URL(window.location);
  const initPath = url.searchParams.get('path') || '/';

  // Check auth and get current user
  fetch('/api/me').then(r => {
    if (isUnauthorized(r)) return null;
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

  // Load config to get show_dotfiles default, wipe methods, and tool availability
  fetch('/api/config').then(r => r.json()).then(cfg => {
    if (cfg && typeof cfg.show_dotfiles === 'boolean') {
      state.showDotfiles = cfg.show_dotfiles;
      updateDotfilesBtn();
    }
    if (cfg && Array.isArray(cfg.wipe_methods)) {
      state.wipeMethods = cfg.wipe_methods;
      populateWipeMethodSelect();
    }
    if (cfg && typeof cfg.ffmpeg_available === 'boolean') {
      state.ffmpegAvailable = cfg.ffmpeg_available;
    }
    if (cfg && typeof cfg.unzip_available === 'boolean') {
      state.unzipAvailable = cfg.unzip_available;
    }
    if (cfg && typeof cfg.sevenzip_available === 'boolean') {
      state.sevenzipAvailable = cfg.sevenzip_available;
    }
    if (cfg && typeof cfg.unrar_available === 'boolean') {
      state.unrarAvailable = cfg.unrar_available;
    }
    if (cfg && typeof cfg.tar_available === 'boolean') {
      state.tarAvailable = cfg.tar_available;
    }
  }).catch(() => {}).finally(() => {
    loadFavourites();
    loadProtectedPaths();
    loadDeletePrefs();
    loadDirectory(initPath);
  });

  loadVersion();
  renderUploadManager();

  // Preview navigation buttons (static handlers - direction never changes)
  document.getElementById('preview-prev').onclick = () => navigatePreview(-1);
  document.getElementById('preview-next').onclick = () => navigatePreview(1);

  // Upload button
  document.getElementById('btn-upload').addEventListener('click', () => {
    document.getElementById('file-input').click();
  });
  document.getElementById('upload-expand-btn').addEventListener('click', () => {
    openModal('modal-uploads');
  });
  document.getElementById('fav-add-btn').addEventListener('click', addFavouriteFromCurrentPath);

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

  // Change password button (visible only in the authenticated sidebar)
  const btnChangePwd = document.getElementById('btn-change-password');
  if (btnChangePwd) {
    btnChangePwd.addEventListener('click', openPasswordModal);
  }
  const pwdUpdateBtn = document.getElementById('pwd-update');
  if (pwdUpdateBtn) {
    pwdUpdateBtn.addEventListener('click', submitPasswordChange);
    // Enter in the new-password field submits like any modal form.
    document.getElementById('pwd-new2').addEventListener('keydown', e => {
      if (e.key === 'Enter') { e.preventDefault(); submitPasswordChange(); }
    });
  }
  document.getElementById('file-input').addEventListener('change', e => {
    uploadFiles(Array.from(e.target.files));
    e.target.value = '';
  });

  // Mkdir button
  document.getElementById('btn-mkdir').addEventListener('click', openMkdirModal);
  document.getElementById('mkdir-confirm').addEventListener('click', doMkdir);
  bindEnterToInput('mkdir-name', doMkdir);

  // New file button
  document.getElementById('btn-new-file').addEventListener('click', openNewFileModal);
  document.getElementById('new-file-confirm').addEventListener('click', doNewFile);
  bindEnterToInput('new-file-name', doNewFile);

  // Delete confirm
  document.getElementById('delete-confirm').addEventListener('click', doDelete);

  // Extra delete confirmation checkbox
  document.getElementById('delete-confirm-check').addEventListener('change', e => {
    document.getElementById('delete-confirm').disabled = !e.target.checked;
  });

  // Persist the secure-delete default and algorithm across sessions (stored
  // server-side in ~/.config/filex.yaml via /api/delete-prefs).
  document.getElementById('delete-wipe-check').addEventListener('change', e => {
    if (!state.deletePrefs) state.deletePrefs = {};
    state.deletePrefs.default_wipe = !!e.target.checked;
    saveDeletePrefs();
  });
  document.getElementById('delete-wipe-method').addEventListener('change', e => {
    if (!state.deletePrefs) state.deletePrefs = {};
    if (e.target.value) state.deletePrefs.wipe_method = e.target.value;
    saveDeletePrefs();
  });

  // Navigate confirm
  document.getElementById('navigate-confirm').addEventListener('click', () => {
    if (_navigateModalResolve) {
      const resolve = _navigateModalResolve;
      _navigateModalResolve = null;
      closeModal('modal-navigate', false);
      resolve(true);
    }
  });
  document.querySelectorAll('#modal-navigate [data-close]').forEach(btn => {
    btn.addEventListener('click', () => {
      if (_navigateModalResolve) {
        const resolve = _navigateModalResolve;
        _navigateModalResolve = null;
        resolve(false);
      }
    });
  });

  // Dotfiles toggle. The chosen state is persisted server-side (per-user in
  // ~/.config/filex.yaml, or the config.yaml server default in no-auth mode) so
  // it survives reloads and other browsers.
  document.getElementById('btn-dotfiles').addEventListener('click', () => {
    state.showDotfiles = !state.showDotfiles;
    updateDotfilesBtn();
    loadDirectory(state.currentPath);
    saveDotfiles();
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

  // Slideshow
  document.getElementById('btn-slideshow').addEventListener('click', startSlideshow);

  // Batch thumbnails
  document.getElementById('btn-thumbnails').addEventListener('click', startBatchThumbnails);
  document.getElementById('thumb-progress-cancel').addEventListener('click', () => {
    if (state.batchThumbCancel) state.batchThumbCancel();
  });

  // Font size controls
  document.getElementById('btn-font-dec').addEventListener('click', () => changeFontSize(-1));
  document.getElementById('btn-font-inc').addEventListener('click', () => changeFontSize(+1));

  // Filter input (case-insensitive, debounced 300ms)
  document.getElementById('filter-input').addEventListener('input', e => {
    clearTimeout(_filterTimer);
    _filterTimer = setTimeout(() => {
      _filterTimer = null;
      state.filterText = e.target.value;
      document.getElementById('filter-clear').classList.toggle('visible', !!state.filterText);
      renderFileList();
      // Filtering can hide every selected file; the row buttons (extract,
      // batch thumbnails, …) are driven from this state so they must follow.
      updateSelectionButtons();
    }, 300);
  });
  document.getElementById('filter-clear').addEventListener('click', () => {
    const input = document.getElementById('filter-input');
    input.value = '';
    state.filterText = '';
    document.getElementById('filter-clear').classList.remove('visible');
    renderFileList();
    updateSelectionButtons();
    input.focus();
  });

  // Select-all checkbox
  document.getElementById('select-all').addEventListener('change', e => {
    const checked = e.target.checked;
    const targets = state.filterText ? getFilteredEntries() : state.entries;
    targets.forEach(entry => {
      if (checked) state.selectedFiles.add(entry.path);
      else state.selectedFiles.delete(entry.path);
    });
    renderFileList();
    updateSelectionButtons();
  });

  // Select by extension
  document.getElementById('btn-select-ext').addEventListener('click', openSelectExtModal);
  document.getElementById('ext-select-all-exts').addEventListener('click', () => {
    document.querySelectorAll('.ext-cb').forEach(cb => cb.checked = true);
  });
  document.getElementById('ext-deselect-all-exts').addEventListener('click', () => {
    document.querySelectorAll('.ext-cb').forEach(cb => cb.checked = false);
  });
  document.getElementById('ext-select-btn').addEventListener('click', () => applyExtSelection(true));
  document.getElementById('ext-deselect-btn').addEventListener('click', () => applyExtSelection(false));

  // Sort headers
  document.querySelectorAll('.file-table th[data-sort]').forEach(th => {
    th.addEventListener('click', () => setSort(th.dataset.sort));
    // Keyboard-operable: headers are div-like table cells, so make them
    // focusable buttons and honour Enter/Space (a11y).
    th.setAttribute('tabindex', '0');
    th.setAttribute('role', 'button');
    th.addEventListener('keydown', e => {
      if (e.key === 'Enter' || e.key === ' ') {
        e.preventDefault();
        setSort(th.dataset.sort);
      }
    });
  });

  // Editor save
  document.getElementById('editor-save').addEventListener('click', saveEditor);

  // Video editor
  function updateAddIntervalDisabled() {
    document.getElementById('video-editor-add-interval').disabled =
      state.videoEditorStartTime == null || state.videoEditorEndTime == null;
  }

  async function snapVideoTime(which) {
    const video = document.getElementById('video-editor-video');
    const entry = state.videoEditorEntry;
    const raw = video.currentTime;
    let snapped = raw;
    if (entry) {
      try {
        const data = await apiPost('/api/video-snap-time', { path: entry.path, time: raw });
        // data is null on auth failure; keep the raw seek target then.
        if (data && data.prev != null && data.next != null) {
          snapped = which === 'start' ? data.prev : data.next;
        }
      } catch (e) {
        // Snapping is best-effort; keep the raw seek target on errors.
      }
    }
    if (which === 'start') {
      state.videoEditorStartTime = snapped;
      document.getElementById('video-editor-start-time').textContent = formatTime(snapped);
    } else {
      state.videoEditorEndTime = snapped;
      document.getElementById('video-editor-end-time').textContent = formatTime(snapped);
    }
    if (Math.abs(snapped - raw) > 0.1) {
      video.currentTime = snapped;
    }
    updateAddIntervalDisabled();
  }

  document.getElementById('video-editor-set-start').addEventListener('click', () => snapVideoTime('start'));
  document.getElementById('video-editor-set-end').addEventListener('click', () => snapVideoTime('end'));
  document.getElementById('video-editor-add-interval').addEventListener('click', () => {
    const video = document.getElementById('video-editor-video');
    const start = state.videoEditorStartTime != null ? state.videoEditorStartTime : 0;
    const end = state.videoEditorEndTime != null ? state.videoEditorEndTime : video.duration;
    const actualStart = Math.min(start, end);
    const actualEnd = Math.max(start, end);
    if (actualEnd - actualStart < 0.5) {
      toast('Interval too short (minimum 0.5s)', 'error');
      return;
    }
    if (state.videoEditorIntervals.some(i => Math.abs(i.start - actualStart) < 0.01 && Math.abs(i.end - actualEnd) < 0.01)) {
      toast('Duplicate interval', 'error');
      return;
    }
    state.videoEditorIntervals.push({ start: actualStart, end: actualEnd });
    renderVideoEditorIntervals();
    saveVideoIntervals();
    state.videoEditorStartTime = null;
    state.videoEditorEndTime = null;
    document.getElementById('video-editor-start-time').textContent = '—';
    document.getElementById('video-editor-end-time').textContent = '—';
    updateAddIntervalDisabled();
  });
  document.getElementById('video-editor-extract').addEventListener('click', doVideoEditorExtract);
  document.getElementById('video-editor-reload-video').addEventListener('click', () => {
    const video = document.getElementById('video-editor-video');
    if (video && video.src) {
      document.getElementById('video-editor-reload-video').style.display = 'none';
      video.load();
    }
  });

  // Auto-reload video when returning to the page (BFCache or tab switch)
  window.addEventListener('pageshow', function onPageShow() {
    const modal = document.getElementById('modal-video-editor');
    if (modal && modal.classList.contains('open')) {
      const video = document.getElementById('video-editor-video');
      if (video && video.src) {
        video.load();
      }
    }
  });

  // Reload video editor video when tab becomes visible again (handles iOS Safari app switch)
  document.addEventListener('visibilitychange', () => {
    if (!document.hidden) {
      const modal = document.getElementById('modal-video-editor');
      if (modal && modal.classList.contains('open')) {
        const video = document.getElementById('video-editor-video');
        if (video && video.src) {
          video.load();
        }
      }
      // Re-show thumbnail progress if a batch operation was active when tab was suspended
      if (state.batchThumbCancel) {
        const el = document.getElementById('thumb-progress');
        if (el && el.style.display === 'none') {
          el.style.display = 'block';
        }
      }
      // Re-show operation progress (move/copy/delete) if an operation was active
      if (state.opAbort) {
        const el = document.getElementById('op-progress');
        if (el && el.style.display === 'none') {
          el.style.display = 'block';
        }
      }
    }
  });

  // Copy modal controls (delegated so they survive the inline-progress body/footer swap)
  const copyModal = document.getElementById('modal-copy');
  copyModal.addEventListener('click', e => {
    if (e.target.closest('#copy-confirm')) { doCopy(); return; }
    if (e.target.closest('#copy-browser-up')) { folderBrowserUp('copy'); return; }
    if (e.target.closest('[data-close="modal-copy"]')) { closeModal('modal-copy'); return; }
  });
  copyModal.addEventListener('keydown', e => bindDstEnter(e, 'copy-dst', doCopy));

  // Download selected
  document.getElementById('btn-download-sel').addEventListener('click', () => {
    downloadZip([...state.selectedFiles]);
  });

  // Unsupported file dialog
  document.getElementById('unsupported-text').addEventListener('click', openUnsupportedText);
  document.getElementById('unsupported-download').addEventListener('click', downloadUnsupported);

  // Move modal controls (delegated so re-created nodes keep working)
  const moveModal = document.getElementById('modal-move');
  moveModal.addEventListener('click', e => {
    if (e.target.closest('#move-confirm')) { doMove(); return; }
    if (e.target.closest('#move-browser-up')) { folderBrowserUp('move'); return; }
    if (e.target.closest('[data-close="modal-move"]')) { closeModal('modal-move'); return; }
  });
  moveModal.addEventListener('keydown', e => bindDstEnter(e, 'move-dst', doMove));

  // Modal close buttons (data-close attribute)
  document.querySelectorAll('[data-close]').forEach(btn => {
    if (btn.closest('#modal-move, #modal-copy')) return;
    btn.addEventListener('click', () => closeModal(btn.dataset.close));
  });

  // Close modal on backdrop click
  document.querySelectorAll('.modal-backdrop').forEach(backdrop => {
    backdrop.addEventListener('click', e => {
      if (e.target === backdrop && backdrop.id !== 'modal-video-editor' && backdrop.id !== 'modal-extract-progress' && backdrop.id !== 'modal-editor') closeModal(backdrop.id);
    });
  });

  // Sidebar toggle (desktop: collapse/expand; mobile: slide-in overlay)
  const SIDEBAR_KEY = 'filex_sidebar_collapsed';
  document.getElementById('menu-toggle').addEventListener('click', () => {
    if (window.innerWidth <= MOBILE_BREAKPOINT) {
      document.getElementById('sidebar').classList.toggle('open');
      document.getElementById('sidebar-overlay').classList.toggle('visible');
    } else {
      const sidebar = document.getElementById('sidebar');
      const isNowCollapsed = sidebar.classList.toggle('collapsed');
      localStorage.setItem(SIDEBAR_KEY, isNowCollapsed ? '1' : '0');
    }
  });
  document.getElementById('sidebar-overlay').addEventListener('click', () => {
    document.getElementById('sidebar').classList.remove('open');
    document.getElementById('sidebar-overlay').classList.remove('visible');
  });

  // Restore sidebar collapsed state on desktop
  if (window.innerWidth > MOBILE_BREAKPOINT && localStorage.getItem(SIDEBAR_KEY) === '1') {
    document.getElementById('sidebar').classList.add('collapsed');
  }

  // When the viewport crosses the mobile/desktop breakpoint, sync sidebar state
  let lastIsMobile = window.innerWidth <= MOBILE_BREAKPOINT;
  window.addEventListener('resize', () => {
    const isMobile = window.innerWidth <= MOBILE_BREAKPOINT;
    if (isMobile === lastIsMobile) return;
    lastIsMobile = isMobile;
    const sidebar = document.getElementById('sidebar');
    if (isMobile) {
      // Entering mobile: clear desktop collapsed state so the overlay works cleanly
      sidebar.classList.remove('collapsed');
    } else {
      // Entering desktop: restore saved state and ensure overlay is closed
      sidebar.classList.remove('open');
      document.getElementById('sidebar-overlay').classList.remove('visible');
      if (localStorage.getItem(SIDEBAR_KEY) === '1') {
        sidebar.classList.add('collapsed');
      }
    }
  });

  // Browser back/forward
  function resetTransientUiState() {
    document.querySelectorAll('.modal-backdrop.open').forEach(m => closeModal(m.id));
    document.getElementById('sidebar').classList.remove('open');
    document.getElementById('sidebar-overlay').classList.remove('visible');
    document.getElementById('drop-overlay').classList.remove('visible');
  }

  window.addEventListener('popstate', e => {
    resetTransientUiState();
    const path = (e.state && e.state.path) ||
                 new URL(window.location).searchParams.get('path') || '/';
    loadDirectory(path, { addHistory: false });
  });

  // Handle page restoration from bfcache (e.g. browser back button from another site)
  window.addEventListener('pageshow', e => {
    if (e.persisted) {
      if (state.batchThumbCancel) return;
      resetTransientUiState();
      loadDirectory(state.currentPath, { addHistory: false });
      resumeBatchIfActive();
    }
  });

  // Extract progress commands toggle
  document.getElementById('extract-progress-commands-toggle').addEventListener('click', () => {
    const cmdEl = document.getElementById('extract-progress-command');
    const toggle = document.getElementById('extract-progress-commands-toggle');
    const isHidden = cmdEl.style.display === 'none';
    cmdEl.style.display = isHidden ? '' : 'none';
    toggle.innerHTML = isHidden ? '&#9660; Commands' : '&#9654; Commands';
  });

  // Archive extraction
  document.getElementById('archive-extract-confirm').addEventListener('click', doArchiveExtract);
  document.getElementById('archive-extract-password').addEventListener('keydown', e => {
    if (e.key === 'Enter') doArchiveExtract();
  });
  document.getElementById('archive-progress-output-toggle').addEventListener('click', () => {
    const pre = document.getElementById('archive-progress-output');
    const toggle = document.getElementById('archive-progress-output-toggle');
    const isHidden = pre.style.display === 'none';
    pre.style.display = isHidden ? '' : 'none';
    toggle.innerHTML = isHidden ? '&#9660; Output' : '&#9654; Output';
  });

  // Allow closing archive progress only when extraction is complete
  document.querySelectorAll('#modal-archive-progress [data-close]').forEach(btn => {
    btn.addEventListener('click', () => {
      const closeBtn = document.getElementById('archive-progress-close');
      if (closeBtn.style.display !== 'none') {
        closeModal('modal-archive-progress');
      }
    });
  });

  // Allow closing by clicking backdrop only when extraction is complete
  document.getElementById('modal-archive-progress').addEventListener('click', e => {
    if (e.target === e.currentTarget) {
      const closeBtn = document.getElementById('archive-progress-close');
      if (closeBtn.style.display !== 'none') {
        closeModal('modal-archive-progress');
      }
    }
  });

  initDragDrop();

  // Slideshow overlay events
  const slideshowOverlay = document.getElementById('slideshow-overlay');
  slideshowOverlay.addEventListener('click', e => {
    if (e.target === slideshowOverlay || e.target === document.getElementById('slideshow-container') || e.target === document.getElementById('slideshow-img')) {
      toggleSlideshowPause();
    }
  });
  document.getElementById('slideshow-status').addEventListener('click', e => {
    e.stopPropagation();
    toggleSlideshowPause();
  });
  document.getElementById('slideshow-exit').addEventListener('click', stopSlideshow);
  document.getElementById('slideshow-speed-down').addEventListener('click', e => { e.stopPropagation(); adjustSlideshowSpeed(-500); });
  document.getElementById('slideshow-speed-up').addEventListener('click', e => { e.stopPropagation(); adjustSlideshowSpeed(500); });
  document.getElementById('slideshow-zoom-in').addEventListener('click', e => { e.stopPropagation(); slideshowZoomIn(); });
  document.getElementById('slideshow-zoom-out').addEventListener('click', e => { e.stopPropagation(); slideshowZoomOut(); });
  document.getElementById('slideshow-zoom-reset').addEventListener('click', e => { e.stopPropagation(); slideshowZoomReset(); });
  slideshowOverlay.addEventListener('wheel', e => {
    if (e.ctrlKey) {
      e.preventDefault();
      if (e.deltaY < 0) slideshowZoomIn();
      else slideshowZoomOut();
    }
  }, { passive: false });
  slideshowOverlay.addEventListener('mousemove', () => {
    showSlideshowControls();
  });

  // Fullscreen change -> stop slideshow
  document.addEventListener('fullscreenchange', () => {
    if (!document.fullscreenElement && document.getElementById('slideshow-overlay').classList.contains('active')) {
      stopSlideshow();
    }
  });
  document.addEventListener('webkitfullscreenchange', () => {
    if (!document.webkitFullscreenElement && document.getElementById('slideshow-overlay').classList.contains('active')) {
      stopSlideshow();
    }
  });

  // Cancel active operation (sidebar)
  document.getElementById('op-progress-cancel').addEventListener('click', () => {
    if (state.opAbort) state.opAbort();
  });

  // Click sidebar progress to re-show progress modal
  document.getElementById('op-progress').addEventListener('click', (e) => {
    if (e.target.closest('button')) return;
    if (!state.opAbort) return;
    if (state.copyInProgress) {
      openModal('modal-copy');
    } else if (state.moveInProgress) {
      openModal('modal-move');
    } else {
      openOpProgressModal();
    }
  });

  // Progress modal Hide button
  document.getElementById('op-progress-modal-hide').addEventListener('click', () => {
    closeModal('modal-op-progress', false);
  });

  // Progress modal Cancel button
  document.getElementById('op-progress-modal-cancel').addEventListener('click', () => {
    if (state.opAbort) state.opAbort();
  });

  // Recover batch thumbnail progress after page reload
  resumeBatchIfActive();
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
  // Attach synchronously (not via setTimeout): if the popup is reopened before
  // a deferred timer fires, the old closure would still register listeners that
  // closeFolderInfoPopup() can no longer remove, leaking handlers. The guard
  // e.target !== anchorBtn keeps the triggering mousedown from closing it.
  document.addEventListener('mousedown', onOutside);
  document.addEventListener('keydown', onKey);
}
