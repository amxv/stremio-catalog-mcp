// Owner credentials live only in this page's JS memory. No cookies,
// localStorage, sessionStorage, URL parameters, or password autofill.
(() => {
  'use strict';
  let key = '';
  const byId = id => document.getElementById(id);
  const login = byId('login-panel');
  const dashboard = byId('dashboard');
  const pairPanel = byId('pair-panel');
  const deviceList = byId('device-list');
  const message = byId('message');
  const status = (text, error = false) => {
    message.textContent = text || '';
    message.classList.toggle('error', !!error);
  };

  async function request(action, fields = {}) {
    const response = await fetch('/tv/admin', {
      method: 'POST',
      mode: 'same-origin',
      credentials: 'omit',
      cache: 'no-store',
      redirect: 'error',
      headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + key },
      body: JSON.stringify({ action, ...fields })
    });
    let data;
    try { data = await response.json(); }
    catch { throw new Error('Unexpected server response'); }
    if (!response.ok) {
      if (response.status === 401 || response.status === 503) lock();
      throw new Error(data.error || 'Request failed (HTTP ' + response.status + ')');
    }
    return data;
  }

  function lock() {
    key = '';
    login.hidden = false;
    dashboard.hidden = true;
    byId('setup-key').value = '';
    byId('pair-code').value = '';
  }

  function render(data) {
    const devices = Array.isArray(data.devices) ? data.devices : [];
    deviceList.replaceChildren();
    if (devices.length === 0) {
      const empty = document.createElement('p');
      empty.className = 'device-empty';
      empty.textContent = 'No TV paired yet.';
      deviceList.append(empty);
    }
    devices.forEach(device => {
      const row = document.createElement('div');
      row.className = 'device';
      const info = document.createElement('div');
      const label = document.createElement('div');
      label.className = 'device-label';
      label.textContent = device.name || 'Android TV';
      const meta = document.createElement('div');
      meta.className = 'device-meta';
      meta.textContent = 'Paired' + (device.paired_at ? ' · ' + new Date(device.paired_at).toLocaleDateString() : '');
      info.append(label, meta);
      const revoke = document.createElement('button');
      revoke.type = 'button';
      revoke.className = 'secondary danger';
      revoke.textContent = 'Revoke pairing';
      revoke.addEventListener('click', async () => {
        if (!window.confirm('Revoke this TV? The companion app will need to be reset and paired again.')) return;
        revoke.disabled = true;
        try {
          render(await request('remove', { device_id: device.device_id }));
          status('TV pairing revoked. You may now pair a different TV.');
        } catch (error) {
          status(error.message, true);
          revoke.disabled = false;
        }
      });
      row.append(info, revoke);
      deviceList.append(row);
    });
    pairPanel.hidden = devices.length !== 0;
    byId('device-hint').textContent = devices.length > 1
      ? 'Legacy multi-TV pairings detected. Revoke extras before using the TV open command.'
      : devices.length === 1
        ? 'One TV is paired. Revoke it before pairing a replacement.'
        : 'Your TV will appear here as soon as pairing is approved.';
  }

  byId('login-form').addEventListener('submit', async event => {
    event.preventDefault();
    const button = event.currentTarget.querySelector('button[type=submit]');
    key = byId('setup-key').value;
    button.disabled = true;
    status('Connecting…');
    try {
      const data = await request('list');
      byId('setup-key').value = '';
      login.hidden = true;
      dashboard.hidden = false;
      render(data);
      status('');
    } catch (error) {
      status(error.message, true);
    } finally {
      button.disabled = false;
    }
  });

  byId('pair-form').addEventListener('submit', async event => {
    event.preventDefault();
    const button = event.currentTarget.querySelector('button[type=submit]');
    const code = byId('pair-code').value.trim();
    if (!/^\d{8}$/.test(code)) { status('Enter the eight-digit code on the TV.', true); return; }
    button.disabled = true;
    status('Approving pairing…');
    try {
      render(await request('pair', { code }));
      byId('pair-code').value = '';
      status('TV paired. You can now ask ChatGPT to open a title on it.');
    } catch (error) {
      status(error.message, true);
    } finally {
      button.disabled = false;
    }
  });
  byId('lock').addEventListener('click', () => { lock(); status('Settings locked.'); });
})();
