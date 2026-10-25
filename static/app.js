import { api } from './js/api.js';
import { inventory } from './js/inventory.js';
import { scanner } from './js/scanner.js';
import { shopping } from './js/shopping.js';
import { recipes } from './js/recipes.js';
import { calendar } from './js/calendar.js';
import { meals } from './js/meals.js';

// Merge parts by copying property descriptors (object spread would evaluate
// getters once and break reactivity). Duplicate keys are a bug: fail loudly.
function compose(...parts) {
  const out = {};
  for (const part of parts) {
    for (const [k, d] of Object.entries(Object.getOwnPropertyDescriptors(part))) {
      if (Object.prototype.hasOwnProperty.call(out, k)) throw new Error('duplicate app key: ' + k);
      Object.defineProperty(out, k, d);
    }
  }
  return out;
}

function app() {
  return compose({
    // State
    darkMode: localStorage.getItem('km-dark-mode') === 'true',

    activeTab: 'inventory',

    tabs: [
      { id: 'inventory', label: 'Pantry', icon: '🥫' },
      { id: 'shopping', label: 'Shopping', icon: '🛒' },
      { id: 'recipes', label: 'Recipes', icon: '📖' },
      { id: 'calendar', label: 'Calendar', icon: '📅' },
      { id: 'history', label: 'History', icon: '📜' },
      { id: 'meals', label: 'Meals', icon: '🍳' },
    ],

    modal: null,

    toast: '',

    _toastTimer: null,

    toastStyle: '',

    toastBadge: '',

    // Auth
    currentUser: null,

    toggleDark() {
      this.darkMode = !this.darkMode;
      document.documentElement.classList.toggle('dark', this.darkMode);
      localStorage.setItem('km-dark-mode', this.darkMode);
    },

    // Safe navigation helpers for templates
    safeGet(obj, path, fallback) {
      if (!obj) return fallback;
      return this.safeGet(this.parsePath(obj, path), path, fallback);
    },

    parsePath(obj, path) {
      if (!path || typeof path !== 'string') return obj;
      const parts = path.split('.');
      let result = obj;
      for (const part of parts) {
        if (result === null || result === undefined) return null;
        result = result[part];
      }
      return result;
    },

    async init() {
      this.calendarWeekStart = this.getMonday(new Date());
      // Determine base path for API endpoints (empty if running directly, /kitchen-manager if behind Traefik)
      this.basePath = window.location.pathname.startsWith('/kitchen-manager') ? '/kitchen-manager' : '';
      this.baseUrl = this.basePath;
      try { this.pendingChecks = JSON.parse(localStorage.getItem(this.pendingKey()) || '{}') || {}; } catch { this.pendingChecks = {}; }
      if ('serviceWorker' in navigator) {
        navigator.serviceWorker.register(this.basePath + '/sw.js', { scope: this.basePath + '/' }).catch(() => {});
      }
      window.addEventListener('online', () => { this.isOnline = true; this.flushPendingChecks(); });
      window.addEventListener('offline', () => { this.isOnline = false; });
      this.fetchMe();
      // One failed loader (e.g. offline) must not abort the rest.
      const safe = async (fn) => { try { await fn(); } catch {} };
      await safe(() => this.fetchShoppingList());
      await safe(() => this.fetchInventory());
      await safe(() => this.fetchExpiring());
      await safe(() => this.fetchSettings());
      await safe(() => this.fetchRecipes());
      await safe(() => this.fetchCalendar());
      await safe(async () => {
        const uRes = await fetch(this.baseUrl + '/api/units');
        this.unitGroups = await uRes.json();
      });
      this.initWebSocket();
      this.flushPendingChecks();
    },

    async switchTab(tab) {
      this.activeTab = tab;
      window.scrollTo({ top: 0, behavior: 'instant' });
      if (tab === 'inventory') { await this.fetchInventory(); await this.fetchExpiring(); }
      if (tab === 'shopping') await this.fetchShoppingList();
      if (tab === 'recipes') await this.fetchRecipes();
      if (tab === 'calendar') { await this.fetchCalendar(); await this.fetchRecipes(); }
      if (tab === 'history') await this.fetchHistory();
      if (tab === 'meals') { await this.fetchMeals(); await this.fetchMealStats(); }
    },

    initWebSocket() {
      let delay = 1000;
      const connect = () => {
        const proto = location.protocol === 'https:' ? 'wss' : 'ws';
        const ws = new WebSocket(`${proto}://${location.host}${this.basePath}/ws`);
        ws.onmessage = (e) => {
          try {
            const msg = JSON.parse(e.data);
            if (msg.type === 'inventory_updated') { this.fetchInventory(); this.fetchExpiring(); }
            if (msg.type === 'shopping_updated') this.fetchShoppingList();
            if (msg.type === 'recipes_updated') { this.fetchRecipes(); this.fetchExpiring(); }
            if (msg.type === 'calendar_updated') this.fetchCalendar();
          } catch {}
        };
        ws.onopen = () => { delay = 1000; this.flushPendingChecks(); };
        ws.onclose = () => { setTimeout(connect, delay); delay = Math.min(delay * 2, 30000); };
        ws.onerror = () => ws.close();
      };
      connect();
    },
  }, api(), inventory(), scanner(), shopping(), recipes(), calendar(), meals());
}

window.app = app; // index.html uses x-data="app()"
