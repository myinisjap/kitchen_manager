export function api() {
  return {
    showToast(msg, style = '', badge = '') {
      clearTimeout(this._toastTimer);
      this.toast = msg;
      this.toastStyle = style;
      this.toastBadge = badge;
      this._toastTimer = setTimeout(() => { this.toast = ''; this.toastStyle = ''; this.toastBadge = ''; }, style === 'error' ? 5000 : 2500);
    },

    // All mutating requests go through here. Returns the Response on 2xx, else null
    // (after reporting the error), so callers must not update local state on null.
    // If onNetworkError is given, it is called instead of reporting when fetch itself rejects.
    async apiFetch(path, opts = {}, { errorPrefix = 'Save failed', onError = null, onNetworkError = null } = {}) {
      const report = (m) => onError ? onError(m) : this.showToast(errorPrefix + ': ' + m, 'error');
      let res;
      try {
        res = await fetch(this.baseUrl + path, opts);
      } catch (e) {
        if (onNetworkError) { onNetworkError(); return null; }
        report('network error');
        return null;
      }
      if (!res.ok) {
        let msg = res.statusText || ('HTTP ' + res.status);
        try {
          const text = (await res.text()).trim();
          if (text) {
            try { const j = JSON.parse(text); msg = (j && j.error) || msg; }
            catch { msg = text.slice(0, 200); }
          }
        } catch {}
        report(msg);
        return null;
      }
      return res;
    },

    async fetchMe() {
      try {
        const res = await fetch(this.baseUrl + '/api/me');
        if (res.ok) {
          const data = await res.json();
          this.currentUser = data.email || null;
        }
      } catch (_) {}
    },

    async logout() {
      const res = await this.apiFetch('/auth/logout', { method: 'POST', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: 'Sign out failed' });
      if (!res) return;
      try { localStorage.removeItem(this.pendingKey()); } catch {}
      this.pendingChecks = {};
      try {
        if ('caches' in window) {
          const keys = await caches.keys();
          await Promise.all(keys.filter(k => k.startsWith('km-')).map(k => caches.delete(k)));
        }
        if (navigator.serviceWorker && navigator.serviceWorker.controller) navigator.serviceWorker.controller.postMessage('clear');
      } catch {}
      window.location.href = this.baseUrl + '/auth/login';
    },
  };
}
