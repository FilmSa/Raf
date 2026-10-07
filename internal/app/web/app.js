'use strict';
const $ = (id) => document.getElementById(id);
const icon = (name) => `<svg aria-hidden="true"><use href="#i-${name}"/></svg>`;
const escapeHTML = (value) => String(value ?? '').replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const statusNames = {
  success: 'Доступ разрешён', unknown_uid: 'Неизвестная карта', face_mismatch: 'Лицо не совпало',
  no_face_detected: 'Лицо не найдено', multiple_faces: 'Несколько лиц на фото', inactive_user: 'Карта заблокирована',
  invalid_uid: 'Некорректный UID', invalid_request: 'Некорректный запрос', service_unavailable: 'Распознавание недоступно',
  server_error: 'Ошибка сервера', unauthorized_device: 'Устройство не авторизовано',
};
const state = { page: 'journal', users: [], offset: 0, total: 0, poll: null, healthAt: 0, refreshing: false,
  authenticated: false, userFingerprint: '', logSerial: 0, previewURL: null, deleteUser: null, failedPoll: false };
const timeFormat = new Intl.DateTimeFormat('ru-RU', { hour:'2-digit', minute:'2-digit', second:'2-digit', timeZone:'Europe/Moscow' });
const dateFormat = new Intl.DateTimeFormat('ru-RU', { day:'2-digit', month:'short', year:'numeric', timeZone:'Europe/Moscow' });
function eventCount(n) { const mod100 = n % 100, mod10 = n % 10; const word = mod100 >= 11 && mod100 <= 14 ? 'событий' : mod10 === 1 ? 'событие' : mod10 >= 2 && mod10 <= 4 ? 'события' : 'событий'; return `${n} ${word}`; }

async function api(path, options = {}) {
  const response = await fetch(path, { credentials:'same-origin', ...options });
  const data = await response.json();
  if (!response.ok) {
    if (response.status === 401 && path !== '/api/admin/login') showLogin();
    throw new Error(data.message || 'Не удалось выполнить запрос');
  }
  return data;
}
function toast(message, error = false) {
  $('toast').textContent = message; $('toast').classList.toggle('error', error); $('toast').hidden = false;
  clearTimeout(toast.timer); toast.timer = setTimeout(() => { $('toast').hidden = true; }, 4500);
}
function setBusy(form, busy) {
  form.querySelectorAll('button,input').forEach((element) => { element.disabled = busy; });
  form.setAttribute('aria-busy', String(busy));
}
function showLogin() {
  state.authenticated = false; clearInterval(state.poll); state.poll = null;
  state.logSerial++; state.userFingerprint = ''; state.users = [];
  $('app-view').hidden = true; $('login-view').hidden = false;
  $('user-dialog').close(); $('confirm-dialog').close(); $('password').value = '';
  if (state.previewURL) { URL.revokeObjectURL(state.previewURL); state.previewURL = null; }
}
async function showApp() {
  state.authenticated = true; $('login-view').hidden = true; $('app-view').hidden = false;
  navigate(); await refresh(true);
  if (!state.authenticated) return;
  clearInterval(state.poll); state.poll = setInterval(() => { if (!document.hidden) refresh(); }, 5000);
}
function navigate() {
  const page = location.hash.slice(1);
  state.page = ['journal','users','terminal'].includes(page) ? page : 'journal';
  const names = {journal:'Журнал событий',users:'Сотрудники',terminal:'Проверка доступа'};
  ['journal','users','terminal'].forEach((name) => { $(`page-${name}`).hidden = name !== state.page; });
  document.querySelectorAll('.nav-link').forEach((link) => {
    const active = link.dataset.page === state.page;
    link.classList.toggle('active', active);
    if (active) link.setAttribute('aria-current','page'); else link.removeAttribute('aria-current');
  });
  $('breadcrumb-page').textContent = names[state.page]; document.title = `${names[state.page]} — Контур`;
  $('sidebar').classList.remove('open');
}
function badge(status) {
  return `<span class="badge ${status === 'success' ? '' : 'denied'}">${icon(status === 'success' ? 'check' : 'close')}${escapeHTML(statusNames[status] || status)}</span>`;
}
function initials(name) { return name.split(/\s+/).filter(Boolean).slice(0,2).map((word) => word[0]).join('').toUpperCase() || '?'; }
function person(name, subtitle) {
  return `<div class="person-cell"><span class="avatar">${escapeHTML(initials(name))}</span><span><strong>${escapeHTML(name || 'Неизвестный сотрудник')}</strong><small>${escapeHTML(subtitle)}</small></span></div>`;
}
function emptyRow(cols, title, description) {
  return `<tr class="empty-row"><td colspan="${cols}">${icon('card')}<strong>${escapeHTML(title)}</strong><p>${escapeHTML(description)}</p></td></tr>`;
}
async function loadLogs() {
  const serial = ++state.logSerial;
  const params = new URLSearchParams({limit:'50',offset:String(state.offset)});
  ['q','status','date'].forEach((key,index) => { const value = $(['log-search','log-status','log-date'][index]).value; if (value) params.set(key,value); });
  const data = await api(`/api/admin/logs?${params}`);
  if (serial !== state.logSerial || !state.authenticated) return;
  state.total = data.total; $('event-count').textContent = data.total;
  $('logs-body').innerHTML = data.items.length ? data.items.map((entry) => {
    const date = new Date(entry.timestamp);
    return `<tr><td class="date-cell"><strong>${escapeHTML(timeFormat.format(date))}</strong><small>${escapeHTML(dateFormat.format(date))}</small></td><td>${person(entry.full_name, entry.full_name ? 'Контроль доступа' : 'Профиль не найден')}</td><td><code class="uid">${escapeHTML(entry.card_uid || '—')}</code></td><td>${badge(entry.result)}</td></tr>`;
  }).join('') : emptyRow(4, 'Событий пока нет', 'Приложите карту к устройству или выполните тестовый проход.');
  $('log-range').textContent = data.total ? `События: ${state.offset + 1}–${state.offset + data.items.length} из ${data.total}` : 'Нет событий по выбранным условиям';
  $('previous-page').disabled = state.offset === 0; $('next-page').disabled = state.offset + 50 >= data.total;
}
function renderUsers() {
  const query = $('user-search').value.trim().toLocaleLowerCase('ru');
  const users = state.users.filter((user) => `${user.full_name} ${user.card_uid}`.toLocaleLowerCase('ru').includes(query));
  $('users-count').textContent = state.users.length;
  $('users-body').innerHTML = users.length ? users.map((user) => `<tr><td>${person(user.full_name,`Сотрудник #${user.id}`)}</td><td><code class="uid">${escapeHTML(user.card_uid)}</code></td><td><span class="badge ${user.is_active ? '' : 'blocked'}">${icon(user.is_active ? 'check' : 'lock')}${user.is_active ? 'Активен' : 'Заблокирован'}</span></td><td>${escapeHTML(dateFormat.format(new Date(user.created_at)))}</td><td><div class="table-actions"><button class="button secondary small-button" data-toggle-user="${Number(user.id)}">${user.is_active ? 'Блокировать' : 'Разблокировать'}</button><button class="icon-button" data-delete-user="${Number(user.id)}" aria-label="Удалить ${escapeHTML(user.full_name)}" title="Удалить">${icon('trash')}</button></div></td></tr>`).join('') : emptyRow(5, query ? 'Сотрудники не найдены' : 'Добавьте первого сотрудника', query ? 'Попробуйте другое имя или UID.' : 'Понадобятся UID карты и фотография лица.');
}
async function loadUsers(force = false) {
  const data = await api('/api/admin/users'); if (!state.authenticated) return;
  const fingerprint = JSON.stringify(data);
  if (force || fingerprint !== state.userFingerprint) { state.users = data; state.userFingerprint = fingerprint; renderUsers(); }
}
async function loadStats() {
  const data = await api('/api/admin/stats'); if (!state.authenticated) return;
  ['success','denied','users','active'].forEach((key) => { $(`stat-${key}`).textContent = data[key]; });
  $('activity-total').textContent = eventCount(data.success + data.denied);
  const maximum = Math.max(1,...data.hourly);
  const currentHour = Number(new Intl.DateTimeFormat('en-GB',{hour:'2-digit',hourCycle:'h23',timeZone:'Europe/Moscow'}).format(new Date()));
  $('activity-bars').replaceChildren(...data.hourly.map((value,hour) => {
    const bar = document.createElement('div'); bar.className = `activity-bar${hour === currentHour ? ' current' : ''}`;
    bar.style.height = `${Math.max(4, value / maximum * 100)}%`; bar.title = `${String(hour).padStart(2,'0')}:00 · ${eventCount(value)}`; return bar;
  }));
  $('activity-bars').setAttribute('aria-label', `События по часам МСК: ${data.hourly.join(', ')}`);
}
async function loadHealth() {
  const data = await api('/api/admin/health'); if (!state.authenticated) return;
  const ready = data.database && data.face_service;
  $('system-state').classList.toggle('warning',!ready);
  $('system-state').lastElementChild.textContent = ready ? 'Система готова к работе' : 'Распознавание недоступно';
  $('device-key-note').textContent = data.device_key_required ? 'Для платы требуется заголовок X-Device-Key. В этой панели проверка доступна по сессии администратора.' : 'Дополнительный ключ устройства не настроен.';
  state.healthAt = Date.now();
}
async function refresh(force = false) {
  if (!state.authenticated || state.refreshing) return;
  state.refreshing = true;
  try {
    const requests = [loadStats(), loadUsers(force)];
    if (state.page === 'journal' || force) requests.push(loadLogs());
    if (Date.now() - state.healthAt > 30000 || force) requests.push(loadHealth());
    const results = await Promise.allSettled(requests);
    const failed = results.find((result) => result.status === 'rejected');
    if (failed) throw failed.reason;
    if (state.authenticated) { $('last-update').textContent = `Обновлено в ${timeFormat.format(new Date())} МСК`; state.failedPoll = false; }
  } catch (error) {
    if (!state.failedPoll && state.authenticated) toast(error.message,true);
    state.failedPoll = true;
    $('system-state').classList.add('warning'); $('system-state').lastElementChild.textContent = 'Нет связи с сервером';
    $('last-update').textContent = 'Данные не обновлены · проверка связи';
  } finally { state.refreshing = false; }
}
function debounce(fn,delay = 250) { let timer; return () => { clearTimeout(timer); timer = setTimeout(fn,delay); }; }
async function applyFilters() { state.offset = 0; try { await loadLogs(); } catch (error) { toast(error.message,true); } }
function openUserDialog() {
  $('user-form').reset(); $('user-error').textContent = ''; $('photo-label').textContent = 'Выберите фотографию';
  $('photo-preview').hidden = true; $('upload-icon').hidden = false;
  if (state.previewURL) { URL.revokeObjectURL(state.previewURL); state.previewURL = null; }
  $('user-dialog').showModal(); $('full-name').focus();
}
document.querySelectorAll('[data-open-user]').forEach((button) => button.addEventListener('click',openUserDialog));
document.querySelectorAll('[data-close-dialog]').forEach((button) => button.addEventListener('click',() => $('user-dialog').close()));
document.querySelectorAll('.uid-input').forEach((input) => input.addEventListener('input',() => { input.value = input.value.toUpperCase(); }));
$('user-photo').addEventListener('change',() => {
  const file = $('user-photo').files[0]; $('user-error').textContent = '';
  if (state.previewURL) URL.revokeObjectURL(state.previewURL);
  state.previewURL = null; $('photo-preview').hidden = true; $('upload-icon').hidden = false;
  $('photo-label').textContent = file?.name || 'Выберите фотографию';
  if (!file) return;
  if (file.size > 2 * 1024 * 1024) { $('user-error').textContent = 'Файл должен быть не больше 2 МБ'; $('user-photo').value = ''; return; }
  state.previewURL = URL.createObjectURL(file); $('photo-preview').src = state.previewURL;
  $('photo-preview').hidden = false; $('upload-icon').hidden = true;
});
$('user-form').addEventListener('submit',async (event) => {
  event.preventDefault(); const form = event.currentTarget; const data = new FormData(form);
  setBusy(form,true); $('user-error').textContent = ''; $('save-user').textContent = 'Обработка фотографии…';
  try { await api('/api/admin/users',{method:'POST',body:data}); $('user-dialog').close(); toast('Сотрудник добавлен'); state.userFingerprint = ''; await refresh(true); }
  catch (error) { $('user-error').textContent = error.message; }
  finally { setBusy(form,false); $('save-user').textContent = 'Сохранить сотрудника'; }
});
$('users-body').addEventListener('click',async (event) => {
  const toggle = event.target.closest('[data-toggle-user]');
  if (toggle) {
    const user = state.users.find((item) => item.id === Number(toggle.dataset.toggleUser)); if (!user) return;
    toggle.disabled = true;
    try { await api(`/api/admin/users/${user.id}`,{method:'PATCH',headers:{'Content-Type':'application/json'},body:JSON.stringify({is_active:!user.is_active})}); toast(user.is_active ? 'Карта заблокирована' : 'Доступ восстановлен'); await loadUsers(true); await loadStats(); }
    catch (error) { toast(error.message,true); toggle.disabled = false; }
  }
  const button = event.target.closest('[data-delete-user]');
  if (button) {
    state.deleteUser = state.users.find((item) => item.id === Number(button.dataset.deleteUser)); if (!state.deleteUser) return;
    $('delete-description').textContent = `${state.deleteUser.full_name} · ${state.deleteUser.card_uid}`; $('confirm-dialog').showModal();
  }
});
['cancel-delete','cancel-delete-x'].forEach((id) => $(id).addEventListener('click',() => $('confirm-dialog').close()));
$('confirm-delete').addEventListener('click',async () => {
  if (!state.deleteUser) return; $('confirm-delete').disabled = true;
  try { await api(`/api/admin/users/${state.deleteUser.id}`,{method:'DELETE'}); $('confirm-dialog').close(); toast('Сотрудник удалён'); await loadUsers(true); await loadStats(); }
  catch (error) { toast(error.message,true); }
  finally { $('confirm-delete').disabled = false; state.deleteUser = null; }
});
$('access-form').addEventListener('submit',async (event) => {
  event.preventDefault(); const form = event.currentTarget; const data = new FormData(form); setBusy(form,true);
  $('access-result').hidden = true;
  try {
    const result = await api('/api/v1/access',{method:'POST',body:data});
    $('access-result').classList.toggle('error',!result.access); $('access-result').hidden = false;
    $('access-result').innerHTML = `<strong>${escapeHTML(statusNames[result.status] || result.status)}</strong><pre>${escapeHTML(JSON.stringify(result,null,2))}</pre>`;
    await loadStats();
  } catch (error) { toast(error.message,true); }
  finally { setBusy(form,false); }
});
$('login-form').addEventListener('submit',async (event) => {
  event.preventDefault(); const form = event.currentTarget; const password = $('password').value;
  setBusy(form,true); $('login-error').textContent = '';
  try { await api('/api/admin/login',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({password})}); $('password').value = ''; await showApp(); }
  catch (error) { $('login-error').textContent = error.message; }
  finally { setBusy(form,false); }
});
$('logout').addEventListener('click',async () => { try { await api('/api/admin/logout',{method:'POST'}); showLogin(); } catch (error) { toast(error.message,true); } });
$('user-search').addEventListener('input',renderUsers);
$('log-search').addEventListener('input',debounce(applyFilters));
['log-date','log-status'].forEach((id) => $(id).addEventListener('change',applyFilters));
$('reset-filters').addEventListener('click',() => { ['log-search','log-status','log-date'].forEach((id) => { $(id).value = ''; }); applyFilters(); });
$('previous-page').addEventListener('click',async () => { state.offset = Math.max(0,state.offset - 50); try { await loadLogs(); } catch (error) { toast(error.message,true); } });
$('next-page').addEventListener('click',async () => { state.offset += 50; try { await loadLogs(); } catch (error) { toast(error.message,true); } });
$('refresh-logs').addEventListener('click',() => refresh(true));
$('menu-toggle').addEventListener('click',() => $('sidebar').classList.toggle('open'));
window.addEventListener('hashchange',() => { navigate(); if (state.authenticated) refresh(true); });
document.addEventListener('visibilitychange',() => { if (!document.hidden && state.authenticated) refresh(true); });
(async () => { try { await api('/api/admin/session'); await showApp(); } catch { showLogin(); } })();
