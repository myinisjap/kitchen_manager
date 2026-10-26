export function inventory() {
  return {
    // Inventory
    inventoryItems: [],

    expiringItems: [],

    expiryOpen: false,

    useItUpRecipes: [],

    inventorySearch: '',

    inventoryLocation: '',

    locations: [],

    editingItem: null,

    unitGroups: {},

    suggestions: [],

    showSuggestions: false,

    _suggestionTimer: null,

    itemForm: { name: '', quantity: 0, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 0, barcode: '', quantity_per_scan: 1 },

    deductModal: null,   // { group, selectedLocationId, qty }

    // SKU alias merge
    skuMergeDialog: null,  // { barcode, name, quantity, unit, candidates: [], mode: 'find'|'grocery' }

    scannedCode: '',       // Temporary storage for current scan

    // SKU alias management (inventory row expand)
    expandedSkus: {},

    skusByItem: {},

    skuNewForm: { barcode: '', quantity_per_scan: 1 },

    // Check if merge dialog is active
    hasMergeDialog() {
      return this.skuMergeDialog !== null && this.skuMergeDialog !== undefined;
    },

    // Check if merge has candidates
    mergeCandidates() {
      if (!this.skuMergeDialog) return false;
      const candidates = this.skuMergeDialog.candidates;
      return candidates !== null && candidates !== undefined && candidates.length > 0;
    },

    async fetchSuggestions() {
      const q = this.itemForm.name;
      if (!q) { this.suggestions = []; this.showSuggestions = false; return; }
      try {
        const res = await fetch(this.baseUrl + '/api/inventory/suggestions?q=' + encodeURIComponent(q));
        if (!res.ok) { this.suggestions = []; this.showSuggestions = false; return; }
        this.suggestions = await res.json();
        this.showSuggestions = this.suggestions.length > 0;
      } catch {
        this.suggestions = [];
        this.showSuggestions = false;
      }
    },

    selectSuggestion(s) {
      this.itemForm.name = s.name;
      this.itemForm.unit = s.unit;
      this.itemForm.preferred_unit = s.preferred_unit;
      this.itemForm.location = s.location;
      this.itemForm.low_threshold = s.low_threshold;
      this.showSuggestions = false;
    },

    unitDimension(unit) {
      const dims = {
        g: 'mass', kg: 'mass', oz: 'mass', lb: 'mass',
        ml: 'volume', L: 'volume', cup: 'volume', tbsp: 'volume', tsp: 'volume',
        piece: 'count', clove: 'count', can: 'count', jar: 'count', bunch: 'count',
      };
      return dims[unit] || null;
    },

    convertUnits(qty, fromUnit, toUnit) {
      if (fromUnit === toUnit) return qty;
      const toBaseG = { g: 1, kg: 1000, oz: 28.3495, lb: 453.592 };
      const toBaseML = { ml: 1, L: 1000, cup: 236.588, tbsp: 14.787, tsp: 4.929 };
      const dimFrom = this.unitDimension(fromUnit);
      const dimTo = this.unitDimension(toUnit);
      if (!dimFrom || dimFrom !== dimTo || dimFrom === 'count') return null;
      if (dimFrom === 'mass') return qty * toBaseG[fromUnit] / toBaseG[toUnit];
      if (dimFrom === 'volume') return qty * toBaseML[fromUnit] / toBaseML[toUnit];
      return null;
    },

    // --- Inventory ---
    async fetchInventory() {
      const params = new URLSearchParams();
      if (this.inventorySearch) params.set('name', this.inventorySearch);
      if (this.inventoryLocation) params.set('location', this.inventoryLocation);
      const res = await fetch(this.baseUrl + '/api/inventory/grouped?' + params);
      const data = await res.json();
      this.inventoryItems = data;
      if (!this.inventoryLocation) {
        this.locations = [...new Set(data.flatMap(g => g.locations.map(l => l.location)).filter(Boolean))];
      }
    },

    async fetchExpiring() {
      const res = await fetch(this.baseUrl + '/api/inventory/expiring');
      this.expiringItems = await res.json();
      try {
        const r2 = await fetch(this.baseUrl + '/api/recipes/use-it-up');
        if (r2.ok) this.useItUpRecipes = (await r2.json()).recipes || [];
      } catch (e) { /* non-fatal */ }
    },

    expiryWindowDays() {
      const n = parseInt(this.settings.expiry_window_days);
      return n >= 1 ? n : 7;
    },

    async saveExpiryWindow(n) {
      n = parseInt(n);
      if (!(n >= 1 && n <= 365)) { this.showToast('Expiry window must be 1-365 days', 'error'); return; }
      const res = await this.apiFetch('/api/settings', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ expiry_window_days: String(n) }),
      }, { errorPrefix: 'Could not save expiry window' });
      if (!res) return;
      this.settings = await res.json();
      await this.fetchExpiring();
    },

    async openUseItUpRecipe(id) {
      let recipe = (this.recipes || []).find(r => r.id === id);
      if (!recipe) {
        const res = await fetch(this.baseUrl + '/api/recipes/' + id);
        if (!res.ok) return;
        recipe = await res.json();
        if (!recipe.incompatible_ingredients) recipe.incompatible_ingredients = [];
      }
      this.viewRecipe(recipe);
    },

    openAddItem() {
      this.editingItem = null;
      this.itemForm = { name: '', quantity: 0, unit: '', preferred_unit: '', location: '', storage_unit_id: null, expiration_date: '', low_threshold: 0, barcode: '', quantity_per_scan: 1 };
      this.suggestions = [];
      this.showSuggestions = false;
      this.syncPickerRoom();
      this.modal = 'item';
    },

    async editItem(locOrItem) {
      this.suggestions = [];
      this.showSuggestions = false;
      // If this is a location stub (from grouped view), fetch the full item
      if (!('preferred_unit' in locOrItem)) {
        const res = await fetch(this.baseUrl + '/api/inventory/' + locOrItem.id);
        if (!res.ok) return;
        locOrItem = await res.json();
      }
      this.editingItem = locOrItem;
      this.itemForm = {
        ...locOrItem,
        preferred_unit: locOrItem.preferred_unit || '',
        storage_unit_id: locOrItem.storage_unit_id ?? null,
      };
      this.syncPickerRoom();
      this.modal = 'item';
    },

    openDeductModal(group) {
      if (!group.locations.length) return;
      this.deductModal = {
        group,
        selectedLocationId: group.recommended_location_id,
        qty: 1,
      };
      this.modal = 'deduct';
    },

    async confirmDeduct() {
      const { group, selectedLocationId, qty } = this.deductModal;
      const loc = group.locations.find(l => l.id === selectedLocationId);
      if (!loc || qty <= 0) return;
      const res = await this.apiFetch('/api/inventory/' + selectedLocationId + '/deduct', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ quantity: qty, single_lot: true }),
      }, { errorPrefix: 'Deduct failed' });
      if (!res) return;
      let out = {};
      try { out = await res.json(); } catch { /* keep default */ }
      if (out.deducted === 0) {
        this.showToast(`${group.name}: nothing deducted – lot unit can't convert to ${group.unit}`, 'error');
      } else if (out.id === 0) {
        this.showToast(`${group.name}: deducted ${qty} ${group.unit} – now out of stock`);
      } else {
        this.showToast(`${group.name}: deducted ${qty} ${group.unit}`);
      }
      this.modal = null;
      this.deductModal = null;
      await this.fetchInventory();
      await this.fetchExpiring();
    },

    async saveItem() {
      if (!this.itemForm.name) return;
      if (this.editingItem) {
        const payload = { ...this.itemForm };
        for (const k of ['quantity', 'low_threshold', 'quantity_per_scan', 'unit_cost_cents']) {
          if (k in payload && (typeof payload[k] !== 'number' || !Number.isFinite(payload[k]))) delete payload[k];
        }
        const res = await this.apiFetch('/api/inventory/' + this.editingItem.id, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        if (!res) return;
        this.showToast('Item updated');
      } else {
        const res = await this.apiFetch('/api/inventory/', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(this.itemForm),
        });
        if (!res) return;
        this.showToast('Item added');
      }
      this.modal = null;
      await this.fetchInventory();
      await this.fetchExpiring();
    },

    addStockToProduct(group) {
      this.openAddItem();
      Object.assign(this.itemForm, {
        name: group.name, unit: group.unit, preferred_unit: group.preferred_unit || '',
        low_threshold: group.par_level || 0, quantity_per_scan: 1,
      });
    },

    async deleteProduct(group) {
      if (!confirm(`Remove ${group.name} from the pantry?`)) return;
      const res = await this.apiFetch('/api/products/' + group.product_id, { method: 'DELETE', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: 'Delete failed' });
      if (!res) return;
      await this.fetchInventory();
      this.showToast('Product removed');
    },

    async deleteItem(id) {
      if (!confirm('Delete this item?')) return;
      const res = await this.apiFetch('/api/inventory/' + id, { method: 'DELETE', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: 'Delete failed' });
      if (!res) return;
      await this.fetchInventory();
      this.showToast('Item deleted');
    },

    // --- SKU Alias Merge ---
    async mergeWithItem(candidate) {
      const dialog = this.skuMergeDialog;
      const scannedUnit = dialog.unit;
      const targetUnit = candidate.preferred_unit || candidate.unit;
      let mergeQty = dialog.quantity || 1;
      let mergeUnit = scannedUnit;

      // Attempt unit conversion
      if (scannedUnit && targetUnit && scannedUnit !== targetUnit) {
        const dimScanned = this.unitDimension(scannedUnit);
        const dimTarget = this.unitDimension(targetUnit);
        if (dimScanned && dimScanned === dimTarget) {
          const converted = this.convertUnits(mergeQty, scannedUnit, targetUnit);
          if (converted !== null) {
            mergeQty = parseFloat(converted.toFixed(4));
            mergeUnit = targetUnit;
          }
        }
        // If dimensions differ, POST as-is and let the backend handle it
      }

      const mergeRes = await this.apiFetch('/api/skus/item/' + candidate.id, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          barcode: dialog.barcode,
          quantity_per_scan: dialog.quantity || 1,
          quantity: mergeQty,
          unit: mergeUnit,
        }),
      }, { errorPrefix: 'Merge failed' });
      if (mergeRes) {
        this.showToast(`Merged "${dialog.name || dialog.barcode}" → ${candidate.name}`, 'success');
        this.fetchInventory();
      }
      this.skuMergeDialog = null;
      // Resume grocery scanner if in grocery mode
      if (dialog.mode === 'grocery') {
        this.groceryScanPaused = false;
      }
    },

    skipMerge() {
      const dialog = this.skuMergeDialog;
      this.skuMergeDialog = null;
      if (dialog.mode === 'find') {
        // Fall through to the normal add-item form
        this.openAddItem();
        Object.assign(this.itemForm, {
          barcode: dialog.barcode,
          name: dialog.name || '',
          quantity: dialog.quantity || 0,
          unit: dialog.unit || '',
          preferred_unit: dialog.unit || '',
        });
      } else if (dialog.mode === 'grocery') {
        // Fall through to the grocery new-item form
        this.groceryNewItem = { barcode: dialog.barcode, quantity: dialog.quantity || 1, unit: dialog.unit || '' };
        this.groceryItemForm = {
          name: dialog.name || '', quantity: dialog.quantity || 1,
          unit: dialog.unit || '', preferred_unit: dialog.unit || '',
          location: '', expiration_date: '', low_threshold: 0,
          barcode: dialog.barcode, quantity_per_scan: dialog.quantity || 1,
        };
        this.groceryScanPaused = true;
      }
    },

    async toggleSkuExpand(itemId) {
      if (this.expandedSkus[itemId]) {
        this.expandedSkus[itemId] = false;
        return;
      }
      try {
        const res = await fetch(this.baseUrl + '/api/skus/item/' + itemId);
        if (res.ok) this.skusByItem[itemId] = await res.json();
      } catch { /* silent */ }
      this.expandedSkus[itemId] = true;
      this.skuNewForm = { barcode: '', quantity_per_scan: 1 };
    },

    async addSkuAlias(itemId) {
      if (!this.skuNewForm.barcode) return;
      const res = await this.apiFetch('/api/skus/item/' + itemId, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          barcode: this.skuNewForm.barcode,
          quantity_per_scan: this.skuNewForm.quantity_per_scan,
          quantity: 0,
          unit: '',
        }),
      }, { errorPrefix: 'Failed to add alias' });
      if (!res) return;
      this.showToast('Alias added');
      try {
        // Refresh the SKU list in place without toggling visibility
        const listRes = await fetch(this.baseUrl + '/api/skus/item/' + itemId);
        if (listRes.ok) this.skusByItem[itemId] = await listRes.json();
      } catch {}
      this.skuNewForm = { barcode: '', quantity_per_scan: 1 };
    },

    async deleteSkuAlias(itemId, skuId) {
      const res = await this.apiFetch('/api/skus/' + skuId, { method: 'DELETE', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: 'Failed to remove alias' });
      if (!res) return;
      this.skusByItem[itemId] = (this.skusByItem[itemId] || []).filter(s => s.id !== skuId);
      this.showToast('Alias removed');
    },
  };
}
