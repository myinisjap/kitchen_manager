export function shopping() {
  return {
    // Shopping
    shoppingItems: [],

    shoppingForm: { name: '', quantity_needed: 1, unit: '' },

    putAwayForm: { id: null, name: '', quantity: 1, unit: '', location: '', expiration_date: '' },

    putAwayBatch: [],

    // Receipt import
    receiptFile: null,

    receiptItems: null,

    receiptParsing: false,

    receiptError: '',

    isOnline: navigator.onLine,

    pendingChecks: {},

    pendingCount() { return Object.keys(this.pendingChecks).length; },

    pendingKey() { return 'km:' + this.basePath + ':pendingChecks'; },

    // --- Shopping ---
    async fetchShoppingList() {
      try {
        const res = await fetch(this.baseUrl + '/api/shopping/?show_checked=true');
        if (!res.ok) return;
        const items = await res.json();
        if (!Array.isArray(items)) return;
        for (const it of items) {
          if (Object.prototype.hasOwnProperty.call(this.pendingChecks, it.id)) it.checked = this.pendingChecks[it.id];
        }
        this.shoppingItems = items;
      } catch {}
    },

    savePending() {
      try { localStorage.setItem(this.pendingKey(), JSON.stringify(this.pendingChecks)); } catch {}
    },

    queueCheck(id, checked) {
      this.pendingChecks = { ...this.pendingChecks, [id]: checked };
      this.savePending();
    },

    dropPending(id) {
      if (!Object.prototype.hasOwnProperty.call(this.pendingChecks, id)) return;
      const next = { ...this.pendingChecks };
      delete next[id];
      this.pendingChecks = next;
      this.savePending();
    },

    async flushPendingChecks() {
      if (this._flushing || this.pendingCount() === 0 || !navigator.onLine) return;
      this._flushing = true;
      let synced = 0;
      const failures = [];
      try {
        for (const id of Object.keys(this.pendingChecks)) {
          let offline = false;
          const res = await this.apiFetch('/api/shopping/' + id, {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ checked: this.pendingChecks[id] }),
          }, { onNetworkError: () => { offline = true; }, onError: m => failures.push(m) });
          if (offline) break;
          if (res) synced++;
          this.dropPending(id);
        }
      } finally {
        this._flushing = false;
      }
      if (failures.length) this.showToast(failures.length + ' offline change(s) could not be synced', 'error');
      else if (synced) this.showToast('Synced ' + synced + ' offline change(s)');
      await this.fetchShoppingList();
    },

    openAddShoppingItem() {
      this.shoppingForm = { name: '', quantity_needed: 1, unit: '' };
      this.modal = 'shopping';
    },

    async saveShoppingItem() {
      if (!this.shoppingForm.name) return;
      const res = await this.apiFetch('/api/shopping/', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(this.shoppingForm),
      });
      if (!res) return;
      this.modal = null;
      await this.fetchShoppingList();
      this.showToast('Added to list');
    },

    openBought(item) {
      this.putAwayForm = { id: item.id, name: item.name, quantity: item.quantity_needed, unit: item.unit || '', location: '', expiration_date: '' };
      this.modal = 'bought';
    },

    async saveBought() {
      const f = this.putAwayForm;
      const res = await this.apiFetch('/api/shopping/' + f.id + '/bought', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ quantity: f.quantity, unit: f.unit, location: f.location, expiration_date: f.expiration_date }),
      }, { errorPrefix: 'Could not add to pantry' });
      if (!res) return;
      this.modal = null;
      await Promise.all([this.fetchShoppingList(), this.fetchInventory()]);
      this.showToast('Added to pantry');
    },

    openPutAwayChecked() {
      const checked = this.shoppingItems.filter(i => i.checked);
      if (checked.length === 0) {
        this.showToast('No checked items');
        return;
      }
      this.putAwayBatch = checked.map(i => ({ id: i.id, name: i.name, quantity: i.quantity_needed, unit: i.unit || '', location: '', expiration_date: '' }));
      this.modal = 'putAway';
    },

    async savePutAway() {
      const res = await this.apiFetch('/api/shopping/put-away', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items: this.putAwayBatch.map(r => ({ id: r.id, quantity: r.quantity, unit: r.unit, location: r.location, expiration_date: r.expiration_date })) }),
      }, { errorPrefix: 'Could not put away items' });
      if (!res) return;
      const data = await res.json();
      this.modal = null;
      await Promise.all([this.fetchShoppingList(), this.fetchInventory()]);
      let msg = `Put away ${data.put_away.length} item(s)`;
      if (data.skipped.length) msg += '; skipped: ' + data.skipped.map(s => s.name + ' (' + s.reason + ')').join(', ');
      this.showToast(msg);
    },

    async toggleShoppingItem(item) {
      const desired = !item.checked;
      item.checked = desired;
      if (!navigator.onLine) { this.queueCheck(item.id, desired); return; }
      let offline = false;
      const res = await this.apiFetch('/api/shopping/' + item.id, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ checked: desired }),
      }, { onNetworkError: () => { offline = true; } });
      if (offline) { this.queueCheck(item.id, desired); return; }
      if (res) this.dropPending(item.id);
      // On failure, refetch so the checkbox reverts to the server state.
      await this.fetchShoppingList();
    },

    async deleteShoppingItem(id) {
      const res = await this.apiFetch('/api/shopping/' + id, { method: 'DELETE', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: 'Delete failed' });
      if (!res) return;
      await this.fetchShoppingList();
    },

    async clearCheckedItems() {
      const res = await this.apiFetch('/api/shopping/checked', { method: 'DELETE', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: 'Clear failed' });
      if (!res) return;
      await this.fetchShoppingList();
      this.showToast('Cleared checked items');
    },

    async generateFromThresholds() {
      const res = await this.apiFetch('/api/shopping/generate-from-thresholds', { method: 'POST', headers: { 'Content-Type': 'application/json' } });
      if (!res) return;
      const data = await res.json();
      await this.fetchShoppingList();
      this.showToast(`Added ${data.added} low-stock items`);
    },

    openReceiptImport() {
      this.receiptFile = null;
      this.receiptItems = null;
      this.receiptError = '';
      this.receiptParsing = false;
      this.modal = 'receipt';
    },

    async parseReceipt() {
      if (!this.receiptFile) return;
      this.receiptParsing = true;
      this.receiptError = '';
      try {
        const fd = new FormData();
        fd.append('file', this.receiptFile);
        const res = await this.apiFetch('/api/receipt/parse', { method: 'POST', body: fd }, { onError: m => { this.receiptError = m; } });
        if (!res) return;
        const data = await res.json();
        this.receiptItems = data.items.map(item => ({
          ...item,
          _include: true,
          _cost_dollars: item.unit_cost_cents ? (item.unit_cost_cents / 100).toFixed(2) : '',
        }));
      } catch (e) {
        this.receiptError = 'Network error: ' + e.message;
      } finally {
        this.receiptParsing = false;
      }
    },

    async addReceiptToInventory() {
      const selected = this.receiptItems.filter(i => i._include);
      if (!selected.length) return;
      let added = 0, failed = 0;
      for (const item of selected) { // sequential: avoids racing duplicate lines
        const receiptQty = item.quantity || 1;
        const qtyPerScan = item.quantity_per_scan > 0 ? item.quantity_per_scan : null;
        const finalQty = qtyPerScan ? receiptQty * qtyPerScan : receiptQty;
        const payload = {
          name: item.name,
          quantity: finalQty,
          unit: item.unit || 'piece',
          preferred_unit: item.unit || 'piece',
          barcode: item.plu || '',
          unit_cost_cents: Math.round((item._cost_dollars || 0) * 100),
          low_threshold: 0,
          quantity_per_scan: qtyPerScan || 0,
        };
        try {
          const res = await this.apiFetch('/api/inventory/', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
          }, { onError: () => {} });
          if (res) { added++; } else { failed++; }
        } catch (e) { failed++; }
      }
      this.modal = '';
      this.receiptItems = null;
      this.receiptFile = null;
      await Promise.all([this.fetchInventory(), this.fetchShoppingList()]);
      if (failed > 0) {
        this.showToast(`Added ${added} items, ${failed} failed`, 'error');
      } else {
        this.showToast(`Added ${added} items to inventory`);
      }
    },
  };
}
