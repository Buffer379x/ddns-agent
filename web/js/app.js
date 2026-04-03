/* ===================================================================
   DDNS Agent — Alpine.js Application
   =================================================================== */

const api = {
  _token() { return localStorage.getItem('ddns_token'); },

  async request(method, url, body) {
    const headers = { 'Content-Type': 'application/json' };
    const token = this._token();
    if (token) headers['Authorization'] = 'Bearer ' + token;
    try {
      const resp = await fetch(url, {
        method,
        headers,
        body: body !== undefined ? JSON.stringify(body) : undefined,
      });
      if (resp.status === 401) {
        Alpine.store('auth').logout();
        Alpine.store('notify').send('warning', Alpine.store('i18n').t('notify.session_expired'));
        return null;
      }
      const data = await resp.json();
      if (!resp.ok) throw new Error(data.error || 'Request failed');
      return data;
    } catch (e) {
      if (e instanceof TypeError && e.message.includes('fetch')) {
        Alpine.store('notify').send('error', Alpine.store('i18n').t('notify.network_error'));
      } else if (e.message && e.message !== 'Request failed') {
        Alpine.store('notify').send('error', e.message);
      }
      throw e;
    }
  },

  get(url)        { return this.request('GET', url); },
  post(url, body) { return this.request('POST', url, body); },
  put(url, body)  { return this.request('PUT', url, body); },
  del(url)        { return this.request('DELETE', url); },
};

function relativeTime(dateStr) {
  if (!dateStr) return '\u2014';
  const t = Alpine.store('i18n').t.bind(Alpine.store('i18n'));
  const diff = Math.floor((Date.now() - new Date(dateStr).getTime()) / 1000);
  if (diff < 0 || diff < 60) return t('time.just_now');
  if (diff < 3600) return t('time.minutes_ago', { n: Math.floor(diff / 60) });
  if (diff < 86400) return t('time.hours_ago', { n: Math.floor(diff / 3600) });
  return t('time.days_ago', { n: Math.floor(diff / 86400) });
}

function buildHostname(rec) {
  if (!rec) return '';
  return (rec.owner && rec.owner !== '@') ? rec.owner + '.' + rec.domain : rec.domain;
}

/** Host/label part only (e.g. www); @ for apex/root DNS name. */
function subdomainLabel(rec) {
  if (!rec) return '';
  const o = String(rec.owner || '').trim();
  if (!o || o === '@') return '@';
  return o;
}

/** Stable pseudo-random color classes per domain string (same domain → same color). */
const DOMAIN_TAG_PALETTE = [
  'bg-violet-500/15 text-violet-800 dark:text-violet-200 ring-1 ring-inset ring-violet-500/35',
  'bg-sky-500/15 text-sky-800 dark:text-sky-200 ring-1 ring-inset ring-sky-500/35',
  'bg-emerald-500/15 text-emerald-800 dark:text-emerald-200 ring-1 ring-inset ring-emerald-500/35',
  'bg-amber-500/15 text-amber-900 dark:text-amber-200 ring-1 ring-inset ring-amber-500/35',
  'bg-rose-500/15 text-rose-800 dark:text-rose-200 ring-1 ring-inset ring-rose-500/35',
  'bg-cyan-500/15 text-cyan-800 dark:text-cyan-200 ring-1 ring-inset ring-cyan-500/35',
  'bg-fuchsia-500/15 text-fuchsia-800 dark:text-fuchsia-200 ring-1 ring-inset ring-fuchsia-500/35',
  'bg-lime-600/15 text-lime-900 dark:text-lime-200 ring-1 ring-inset ring-lime-600/35',
  'bg-indigo-500/15 text-indigo-800 dark:text-indigo-200 ring-1 ring-inset ring-indigo-500/35',
  'bg-teal-500/15 text-teal-800 dark:text-teal-200 ring-1 ring-inset ring-teal-500/35',
];

function domainTagClass(domain) {
  const s = String(domain || '');
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return DOMAIN_TAG_PALETTE[Math.abs(h) % DOMAIN_TAG_PALETTE.length];
}

function ipVersionLabel(v) {
  let k = String(v || 'ipv4').toLowerCase();
  if (k === 'both') k = 'dual'; // legacy form value; DB may still contain "both"
  const key = { ipv4: 'ipversion.ipv4', ipv6: 'ipversion.ipv6', dual: 'ipversion.dual' }[k] || 'ipversion.ipv4';
  return Alpine.store('i18n').t(key);
}

function statusDotClass(status) {
  const map = { success: 'st-success', error: 'st-error', updating: 'st-updating', pending: 'st-pending' };
  return map[status] || 'st-pending';
}

function logLevelClass(level) {
  const map = {
    SUCCESS: 'log-success', INFO: 'log-info', WARNING: 'log-warning',
    ERROR: 'log-error', CRITICAL: 'log-critical',
  };
  return map[(level || '').toUpperCase()] || '';
}

function logFilterClass(level, active) {
  if (!active) return 'bg-th-surface-high text-th-text-dim';
  const map = {
    SUCCESS: 'bg-green-500/15 text-green-500 dark:text-green-400',
    INFO: 'bg-blue-500/15 text-blue-500 dark:text-blue-400',
    WARNING: 'bg-yellow-500/15 text-yellow-500 dark:text-yellow-400',
    ERROR: 'bg-red-500/15 text-red-500 dark:text-red-400',
    CRITICAL: 'bg-red-600/20 text-red-600 dark:text-red-400',
  };
  return map[level] || 'bg-th-primary-bg text-th-primary-text';
}

function formatLogTime(dateStr) {
  if (!dateStr) return '';
  const d = new Date(dateStr);
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function sortedByHostname(records) {
  return [...records].sort((a, b) => buildHostname(a).localeCompare(buildHostname(b)));
}

document.addEventListener('alpine:init', () => {

  Alpine.store('i18n', {
    locale: 'en',
    messages: {},
    available: [],

    async init() {
      this.locale = localStorage.getItem('ddns_locale') || 'en';
      await this.load(this.locale);
      this.loadAvailable();
    },

    async load(locale) {
      try {
        const resp = await fetch('/api/lang/' + locale);
        if (resp.ok) {
          this.messages = await resp.json();
          this.locale = locale;
          localStorage.setItem('ddns_locale', locale);
        }
      } catch (_) {}
    },

    async loadAvailable() {
      try {
        const resp = await fetch('/api/lang');
        if (resp.ok) this.available = await resp.json();
      } catch (_) {}
    },

    t(key, params) {
      let s = this.messages[key] || key;
      if (params) Object.entries(params).forEach(([k, v]) => { s = s.replace('{' + k + '}', v); });
      return s;
    },
  });

  Alpine.store('theme', {
    mode: localStorage.getItem('ddns_theme') || 'auto',

    get isDark() {
      if (this.mode === 'dark') return true;
      if (this.mode === 'light') return false;
      return window.matchMedia('(prefers-color-scheme: dark)').matches;
    },

    set(mode) {
      this.mode = mode;
      localStorage.setItem('ddns_theme', mode);
      this._apply();
    },

    _apply() { document.documentElement.classList.toggle('dark', this.isDark); },

    init() {
      this._apply();
      window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
        if (this.mode === 'auto') this._apply();
      });
    },
  });

  Alpine.store('notify', {
    toasts: [],
    _counter: 0,

    send(level, message, duration) {
      const id = ++this._counter;
      if (duration === undefined) {
        duration = (level === 'error') ? 0 : (level === 'warning') ? 6000 : 4000;
      }
      this.toasts.push({ id, level, message, visible: true });
      while (this.toasts.length > 5) this.toasts.shift();
      if (duration > 0) setTimeout(() => this.dismiss(id), duration);
    },

    dismiss(id) {
      const t = this.toasts.find(t => t.id === id);
      if (t) {
        t.visible = false;
        setTimeout(() => { this.toasts = this.toasts.filter(x => x.id !== id); }, 300);
      }
    },
  });

  Alpine.store('auth', {
    token: localStorage.getItem('ddns_token'),
    user: null,
    role: localStorage.getItem('ddns_role') || 'viewer',
    version: '',

    get isAdmin() { return this.role === 'admin'; },

    setToken(token) {
      this.token = token;
      if (token) localStorage.setItem('ddns_token', token);
      else localStorage.removeItem('ddns_token');
    },

    setRole(role) {
      this.role = role;
      localStorage.setItem('ddns_role', role);
    },

    logout() {
      this.token = null;
      this.user = null;
      this.role = 'viewer';
      localStorage.removeItem('ddns_token');
      localStorage.removeItem('ddns_role');
    },
  });

  Alpine.store('app', {
    page: 'overview',
    sidebarOpen: false,
  });

  Alpine.data('loginPage', () => ({
    username: '',
    password: '',
    loading: false,
    loginVersion: '',
    errorMsg: '',

    async init() {
      try {
        const resp = await fetch('/api/version');
        if (resp.ok) { const d = await resp.json(); this.loginVersion = d.version || ''; }
      } catch (_) {}
    },

    async submit() {
      if (this.loading || !this.username || !this.password) return;
      this.loading = true;
      this.errorMsg = '';
      try {
        const resp = await fetch('/api/auth/login', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ username: this.username, password: this.password }),
        });
        const data = await resp.json();
        if (!resp.ok) {
          this.errorMsg = data.error || Alpine.store('i18n').t('notify.login_failed');
          return;
        }
        Alpine.store('app').page = 'overview';
        Alpine.store('auth').setToken(data.token);
        Alpine.store('auth').user = data.user;
        Alpine.store('auth').setRole(data.user.role);

        if (data.password_is_default) {
          setTimeout(() => {
            Alpine.store('notify').send('warning', Alpine.store('i18n').t('notify.password_default'), 0);
          }, 500);
        }
      } catch (_) {
        this.errorMsg = Alpine.store('i18n').t('notify.network_error');
      } finally {
        this.loading = false;
      }
    },
  }));

  Alpine.data('mainApp', () => ({
    get navItems() {
      const isAdmin = Alpine.store('auth').isAdmin;
      const items = [
        { page: 'overview',  icon: 'dashboard',  label: 'nav.overview' },
        { page: 'hostnames', icon: 'dns',         label: 'nav.hostnames', adminOnly: false },
        { page: 'logs',      icon: 'terminal',    label: 'nav.logs' },
        { page: 'users',     icon: 'group',       label: 'nav.users', adminOnly: true },
        { page: 'settings',  icon: 'settings',    label: 'nav.settings', adminOnly: true },
      ];
      return items.filter(i => !i.adminOnly || isAdmin);
    },

    stats: { total: 0, active: 0, errors: 0 },
    records: [],
    providers: [],
    selectedProviderFields: [],
    showRecordModal: false,
    editingRecord: null,
    /** Snapshot of parsed provider_config when opening edit; used to keep secrets if password fields are left blank. */
    _recordConfigBackup: null,
    recordForm: { provider: '', domain: '', owner: '@', ip_version: 'ipv4', enabled: true, config: {} },

    logs: [],
    logTotal: 0,
    logLevels: { SUCCESS: true, INFO: true, WARNING: true, ERROR: true, CRITICAL: true },
    logSearch: '',
    _sse: null,

    settingsForm: { refresh_interval: '300', app_timezone: '' },
    webhooks: [],
    showWebhookModal: false,
    editingWebhook: null,
    webhookForm: { name: '', type: 'discord', url: '', events: 'ip_change,error', enabled: true },

    users: [],
    showUserModal: false,
    editingUser: null,
    userForm: { username: '', password: '', role: 'viewer' },

    async init() {
      try {
        const v = await fetch('/api/version');
        if (v.ok) { const d = await v.json(); Alpine.store('auth').version = d.version || ''; }
      } catch (_) {}

      try {
        const me = await api.get('/api/auth/me');
        if (me) { Alpine.store('auth').user = me; Alpine.store('auth').setRole(me.role); }
      } catch (_) {}

      await this.loadOverview();
      this._connectSSE();
    },

    destroy() { this._disconnectSSE(); },

    async navigate(page) {
      Alpine.store('app').page = page;
      Alpine.store('app').sidebarOpen = false;
      try {
        switch (page) {
          case 'overview':  await this.loadOverview(); break;
          case 'hostnames': await Promise.all([this.loadRecords(), this.loadProviders()]); break;
          case 'logs':      await this.loadLogs(); break;
          case 'settings':  await Promise.all([this.loadSettings(), this.loadWebhooks(), this.loadOverview()]); break;
          case 'users':     await this.loadUsers(); break;
        }
      } catch (_) {}
    },

    get pageTitle() {
      const t = Alpine.store('i18n').t.bind(Alpine.store('i18n'));
      const map = {
        overview: t('overview.title'), hostnames: t('hostnames.title'),
        logs: t('logs.title'), settings: t('settings.title'), users: t('users.title'),
      };
      return map[Alpine.store('app').page] || '';
    },

    get sortedRecords() { return sortedByHostname(this.records); },

    get activeRecords() {
      return sortedByHostname(this.records.filter(r => r.enabled !== false));
    },

    get sortedUsers() {
      return [...this.users].sort((a, b) => a.username.localeCompare(b.username));
    },

    isLogFiltered() {
      const allOn = Object.values(this.logLevels).every(v => v);
      return !allOn || this.logSearch.length > 0;
    },

    async loadOverview() {
      try {
        const [status, recs] = await Promise.all([api.get('/api/status'), api.get('/api/records')]);
        if (status) this.stats = { total: status.total_records, active: status.active_records, errors: status.error_records };
        if (recs) this.records = recs;
      } catch (_) {}
    },

    async refreshRecord(id) {
      try { await api.post('/api/records/' + id + '/refresh'); Alpine.store('notify').send('success', Alpine.store('i18n').t('notify.record_refreshed')); } catch (_) {}
      setTimeout(() => this.loadRecords(), 2000);
    },

    async refreshAll() {
      try {
        await api.post('/api/records/refresh-all', {});
        Alpine.store('notify').send('success', Alpine.store('i18n').t('notify.all_refreshed'));
        setTimeout(() => this.loadRecords(), 3000);
      } catch (_) {}
    },

    async loadProviders() {
      try { const d = await api.get('/api/providers'); if (d) this.providers = d; } catch (_) {}
    },

    async loadRecords() {
      try { const d = await api.get('/api/records'); if (d) this.records = d; } catch (_) {}
    },

    providerLabel(name) {
      const p = this.providers.find(x => x.name === name);
      return p ? p.label : name;
    },

    async loadProviderFields(providerName) {
      if (!providerName) { this.selectedProviderFields = []; return; }
      try {
        const d = await api.get('/api/providers/' + providerName + '/fields');
        this.selectedProviderFields = (d && d.fields) ? d.fields : [];
      } catch (_) { this.selectedProviderFields = []; }
    },

    openAddRecord() {
      this.editingRecord = null;
      this._recordConfigBackup = null;
      this.recordForm = { provider: '', domain: '', owner: '@', ip_version: 'ipv4', enabled: true, config: {} };
      this.selectedProviderFields = [];
      this.showRecordModal = true;
    },

    async openEditRecord(rec) {
      this.editingRecord = rec;
      let cfg = {};
      try { cfg = JSON.parse(rec.provider_config || '{}'); } catch (_) {}
      this._recordConfigBackup = JSON.parse(JSON.stringify(cfg));
      await this.loadProviderFields(rec.provider);
      const formCfg = { ...cfg };
      for (const f of this.selectedProviderFields) {
        if (f.type === 'password') formCfg[f.name] = '';
      }
      this.recordForm = {
        provider: rec.provider,
        domain: rec.domain,
        owner: rec.owner,
        ip_version: rec.ip_version,
        enabled: rec.enabled,
        config: formCfg,
      };
      this.showRecordModal = true;
    },

    async onProviderChange() {
      this.recordForm.config = {};
      await this.loadProviderFields(this.recordForm.provider);
    },

    async saveRecord() {
      const cfg = { ...this.recordForm.config };
      if (this.editingRecord && this._recordConfigBackup) {
        for (const f of this.selectedProviderFields) {
          if (f.type !== 'password') continue;
          const v = cfg[f.name];
          if (typeof v !== 'string' || v.trim() === '') {
            if (this._recordConfigBackup[f.name] !== undefined) {
              cfg[f.name] = this._recordConfigBackup[f.name];
            }
          }
        }
      }
      const payload = {
        provider: this.recordForm.provider,
        domain: this.recordForm.domain,
        owner: this.recordForm.owner || '@',
        ip_version: this.recordForm.ip_version,
        enabled: this.recordForm.enabled,
        provider_config: JSON.stringify(cfg),
      };
      const t = Alpine.store('i18n').t.bind(Alpine.store('i18n'));
      try {
        if (this.editingRecord) {
          await api.put('/api/records/' + this.editingRecord.id, payload);
          Alpine.store('notify').send('success', t('notify.record_updated'));
        } else {
          await api.post('/api/records', payload);
          Alpine.store('notify').send('success', t('notify.record_created'));
        }
        this.showRecordModal = false;
        await Promise.all([this.loadRecords(), this.loadOverview()]);
      } catch (_) {}
    },

    async deleteRecord(id) {
      if (!confirm(Alpine.store('i18n').t('hostnames.delete_confirm'))) return;
      try {
        await api.del('/api/records/' + id);
        Alpine.store('notify').send('success', Alpine.store('i18n').t('notify.record_deleted'));
        await Promise.all([this.loadRecords(), this.loadOverview()]);
      } catch (_) {}
    },

    async loadLogs() {
      try {
        const d = await api.get('/api/logs?limit=200');
        if (d) { this.logs = d.logs || []; this.logTotal = d.total || 0; }
      } catch (_) {}
    },

    getFilteredLogs() {
      return this.logs.filter(l => {
        if (!this.logLevels[(l.level || '').toUpperCase()]) return false;
        if (this.logSearch) {
          const q = this.logSearch.toLowerCase();
          return l.message.toLowerCase().includes(q) || (l.source && l.source.toLowerCase().includes(q));
        }
        return true;
      });
    },

    async clearLogs() {
      if (!confirm(Alpine.store('i18n').t('logs.clear_confirm'))) return;
      try {
        await api.del('/api/logs');
        this.logs = [];
        this.logTotal = 0;
        Alpine.store('notify').send('success', Alpine.store('i18n').t('notify.logs_cleared'));
      } catch (_) {}
    },

    _connectSSE() {
      const token = localStorage.getItem('ddns_token');
      if (!token) return;
      this._sse = new EventSource('/api/events?token=' + token);
      this._sse.addEventListener('log', (e) => {
        try {
          const entry = JSON.parse(e.data);
          this.logs.unshift(entry);
          if (this.logs.length > 500) this.logs.length = 500;
          this.logTotal++;
        } catch (_) {}
      });
      this._sse.addEventListener('notification', (e) => {
        try {
          const d = JSON.parse(e.data);
          Alpine.store('notify').send(d.level || 'info', d.message);
        } catch (_) {}
      });
      this._sse.onerror = () => {
        this._disconnectSSE();
        setTimeout(() => this._connectSSE(), 5000);
      };
    },

    _disconnectSSE() {
      if (this._sse) { this._sse.close(); this._sse = null; }
    },

    async loadSettings() {
      try {
        const d = await api.get('/api/settings');
        if (d) {
          this.settingsForm = {
            refresh_interval: d.refresh_interval || '300',
            app_timezone: d.app_timezone || '',
          };
        }
      } catch (_) {}
    },

    async saveSettings() {
      try {
        await api.put('/api/settings', this.settingsForm);
        Alpine.store('notify').send('success', Alpine.store('i18n').t('notify.settings_saved'));
      } catch (_) {}
    },

    async loadWebhooks() {
      try { const d = await api.get('/api/webhooks'); if (d) this.webhooks = d; } catch (_) {}
    },

    openAddWebhook() {
      this.editingWebhook = null;
      this.webhookForm = { name: '', type: 'discord', url: '', events: 'ip_change,error', enabled: true };
      this.showWebhookModal = true;
    },

    openEditWebhook(hook) {
      this.editingWebhook = hook;
      this.webhookForm = { name: hook.name, type: hook.type, url: hook.url, events: hook.events, enabled: hook.enabled };
      this.showWebhookModal = true;
    },

    async saveWebhook() {
      const t = Alpine.store('i18n').t.bind(Alpine.store('i18n'));
      try {
        if (this.editingWebhook) {
          await api.put('/api/webhooks/' + this.editingWebhook.id, this.webhookForm);
        } else {
          await api.post('/api/webhooks', this.webhookForm);
        }
        Alpine.store('notify').send('success', t('notify.webhook_created'));
        this.showWebhookModal = false;
        await this.loadWebhooks();
      } catch (_) {}
    },

    async deleteWebhook(id) {
      try {
        await api.del('/api/webhooks/' + id);
        Alpine.store('notify').send('success', Alpine.store('i18n').t('notify.webhook_deleted'));
        await this.loadWebhooks();
      } catch (_) {}
    },

    async testWebhook(id) {
      try {
        await api.post('/api/webhooks/' + id + '/test');
        Alpine.store('notify').send('success', Alpine.store('i18n').t('notify.webhook_test_sent'));
      } catch (_) {}
    },

    async exportConfig() {
      try {
        const token = localStorage.getItem('ddns_token');
        const resp = await fetch('/api/config/export', { headers: { 'Authorization': 'Bearer ' + token } });
        if (!resp.ok) throw new Error('Export failed');
        const blob = await resp.blob();
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url; a.download = 'ddns-agent-config.json';
        document.body.appendChild(a); a.click(); document.body.removeChild(a);
        URL.revokeObjectURL(url);
        Alpine.store('notify').send('success', Alpine.store('i18n').t('notify.config_exported'));
      } catch (e) { Alpine.store('notify').send('error', e.message); }
    },

    async importConfig(event) {
      const file = event.target.files[0];
      if (!file) return;
      if (!confirm(Alpine.store('i18n').t('settings.import_confirm'))) { event.target.value = ''; return; }
      try {
        const text = await file.text();
        const token = localStorage.getItem('ddns_token');
        const resp = await fetch('/api/config/import', {
          method: 'POST',
          headers: { 'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json' },
          body: text,
        });
        if (!resp.ok) { const d = await resp.json(); throw new Error(d.error); }
        Alpine.store('notify').send('success', Alpine.store('i18n').t('notify.config_imported'));
        await this.loadRecords();
      } catch (e) { Alpine.store('notify').send('error', e.message); }
      event.target.value = '';
    },

    async loadUsers() {
      try { const d = await api.get('/api/users'); if (d) this.users = d; } catch (_) {}
    },

    openAddUser() {
      this.editingUser = null;
      this.userForm = { username: '', password: '', role: 'viewer' };
      this.showUserModal = true;
    },

    openEditUser(user) {
      this.editingUser = user;
      this.userForm = { username: user.username, password: '', role: user.role };
      this.showUserModal = true;
    },

    async saveUser() {
      const t = Alpine.store('i18n').t.bind(Alpine.store('i18n'));
      try {
        if (this.editingUser) {
          await api.put('/api/users/' + this.editingUser.id, this.userForm);
          Alpine.store('notify').send('success', t('notify.user_updated'));
        } else {
          await api.post('/api/users', this.userForm);
          Alpine.store('notify').send('success', t('notify.user_created'));
        }
        this.showUserModal = false;
        await this.loadUsers();
      } catch (_) {}
    },

    async deleteUser(id) {
      if (!confirm(Alpine.store('i18n').t('users.delete_confirm'))) return;
      try {
        await api.del('/api/users/' + id);
        Alpine.store('notify').send('success', Alpine.store('i18n').t('notify.user_deleted'));
        await this.loadUsers();
      } catch (_) {}
    },

    signOut() {
      this._disconnectSSE();
      Alpine.store('auth').logout();
    },

    relativeTime,
    buildHostname,
    subdomainLabel,
    domainTagClass,
    ipVersionLabel,
    statusDotClass,
    logLevelClass,
    logFilterClass,
    formatLogTime,
  }));
});
