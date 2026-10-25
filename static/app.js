function app() {
  return {
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
    scannerOpen: false,
    scannerMode: '',
    _codeReader: null,
    _stream: null,
    _scannerActive: false,
    _scanRaf: null,
    groceryScanOpen: false,
    groceryScanPaused: false,
    groceryScanReview: false,
    groceryPendingItems: [],
    _groceryProcessing: false,
    _scanCooldown: false,
    groceryNewItem: null, // prefill object when paused for unknown item
    groceryCameraError: '',
    removeStockScanOpen: false,
    _removeStockStream: null,
    _removeStockRaf: null,
    _removeStockActive: false,
    groceryItemForm: { name: '', quantity: 1, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 0, barcode: '', quantity_per_scan: 1 },
    itemForm: { name: '', quantity: 0, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 0, barcode: '', quantity_per_scan: 1 },
    deductModal: null,   // { group, selectedLocationId, qty }

    // SKU alias merge
    skuMergeDialog: null,  // { barcode, name, quantity, unit, candidates: [], mode: 'find'|'grocery' }
    scannedCode: '',       // Temporary storage for current scan

    // SKU alias management (inventory row expand)
    expandedSkus: {},
    skusByItem: {},
    skuNewForm: { barcode: '', quantity_per_scan: 1 },

    // Auth
    currentUser: null,

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

    // Recipes
    recipes: [],
    recipeTagFilter: '',
    recipeShowArchived: false,
    recipeAvailableOnly: false,
    selectedRecipe: null,
    settings: {},
    recipeServings: {},
    recipeForm: { id: null, name: '', description: '', instructions: '', tags: '', servings: 2, ingredients: [] },
    recipeViewDate: '',
    recipeViewSlot: 'dinner',
    recipeImportTab: 'url',
    recipeImportUrl: '',
    recipeImportFile: null,
    recipeImportText: '',
    recipeImporting: false,
    recipeImportError: '',

    // History
    historyItems: [],
    historyStatsData: null,

    // Meals
    meals: [],
    mealsTotal: 0,
    mealsOffset: 0,
    mealsStats: null,
    mealStatsLoaded: false,
    cookItServings: 2,
    cookItConfirm: null,

    // Calendar
    calendarEntries: [],
    calendarWeekStart: null,
    mealSlots: ['breakfast', 'lunch', 'dinner', 'snack'],
    calendarEntryForm: { id: null, date: '', meal_slot: 'dinner', recipe_id: '', servings: null },

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

    isOnline: navigator.onLine,
    pendingChecks: {},
    pendingCount() { return Object.keys(this.pendingChecks).length; },
    pendingKey() { return 'km:' + this.basePath + ':pendingChecks'; },

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

    async openScanner(mode) {
      if (this._scannerActive) { this.closeScanner(); }
      this.scannerMode = mode;
      this.scannerOpen = true;
      await this.$nextTick();
      if (!this.scannerOpen) return;

      const video = document.getElementById('scanner-video');
      try {
        const stream = await navigator.mediaDevices.getUserMedia({
          video: { facingMode: { ideal: 'environment' }, width: { ideal: 1920 }, height: { ideal: 1080 } }
        });
        if (!this.scannerOpen) { stream.getTracks().forEach(t => t.stop()); return; }
        this._stream = stream;
        video.srcObject = stream;
        await video.play();
        await new Promise(resolve => {
          if (video.readyState >= 2) { resolve(); return; }
          video.addEventListener('canplay', resolve, { once: true });
        });
      } catch (err) {
        this.closeScanner();
        const msg = (err.name === 'NotAllowedError' || err.name === 'PermissionDeniedError')
          ? 'Camera access denied — enter barcode manually'
          : 'Camera unavailable — enter barcode manually';
        this.showToast(msg);
        return;
      }

      this._scannerActive = true;

      if (typeof BarcodeDetector !== 'undefined') {
        const detector = new BarcodeDetector({ formats: ['ean_13','ean_8','upc_a','upc_e','code_128','code_39','qr_code','itf','codabar'] });
        const scan = async () => {
          if (!this._scannerActive) return;
          try {
            const codes = await detector.detect(video);
            if (codes.length > 0) { this.onBarcodeScanned(codes[0].rawValue); return; }
          } catch {}
          this._scanRaf = requestAnimationFrame(scan);
        };
        this._scanRaf = requestAnimationFrame(scan);
      } else {
        this._codeReader = new ZXing.BrowserMultiFormatReader();
        this._codeReader.decodeFromVideoElementContinuously(video, (result, err) => {
          if (result) this.onBarcodeScanned(result.getText());
        });
      }
    },

    closeScanner() {
      this._scannerActive = false;
      if (this._scanRaf) { cancelAnimationFrame(this._scanRaf); this._scanRaf = null; }
      if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
      if (this._stream) { this._stream.getTracks().forEach(t => t.stop()); this._stream = null; }
      this.scannerOpen = false;
      this.scannerMode = '';
    },

    async onBarcodeScanned(code) {
      if (!this.scannerOpen) return; // guard against duplicate ZXing callbacks
      const mode = this.scannerMode;
      this.scannedCode = code;
      this.closeScanner();
      if (mode === 'fill') {
        this.itemForm.barcode = code;
        return;
      }
      // mode === 'find': look up in our inventory first
      try {
        const res = await fetch(this.baseUrl + '/api/inventory/barcode/' + encodeURIComponent(code));
        if (res.ok) {
          const item = await res.json();
          if (item.id === 0) {
            // Known product with no stock left: add a new lot instead of editing one.
            this.openAddItem();
            Object.assign(this.itemForm, {
              name: item.name, unit: item.unit, preferred_unit: item.preferred_unit || '',
              low_threshold: item.low_threshold, barcode: code, quantity_per_scan: item.quantity_per_scan,
            });
            return;
          }
          this.editItem(item);
          return;
        }
      } catch {
        // fall through to Open Food Facts
      }
      // Not in inventory — try Open Food Facts
      let prefill = { barcode: code };
      let scannedName = '', scannedQty = null, scannedUnit = null;
      try {
        const off = await fetch('https://world.openfoodfacts.org/api/v2/product/' + encodeURIComponent(code) + '.json');
        if (off.ok) {
          const data = await off.json();
          if (data.status === 1 && data.product) {
            const p = data.product;
            if (p.product_name) scannedName = p.product_name;
            if (p.product_name) prefill.name = p.product_name;

            const parsed = this.parseOffQuantity(p);
            if (parsed) {
              scannedQty = parsed.qty;
              scannedUnit = parsed.unit;
              prefill.quantity = parsed.qty;
              prefill.unit = parsed.unit;
              prefill.preferred_unit = parsed.unit;
            }
          }
        }
      } catch {
        // silent fallback — open form with just barcode
      }

      // Try to find merge candidates via similar search if we have a name
      if (scannedName && scannedName.trim()) {
        try {
          const similarRes = await fetch(this.baseUrl + '/api/inventory/similar?name=' + encodeURIComponent(scannedName));
          if (similarRes.ok) {
            const candidates = await similarRes.json();
            if (candidates && candidates.length > 0) {
              // Show merge dialog
              this.skuMergeDialog = {
                barcode: code,
                name: scannedName,
                quantity: scannedQty || 1,
                unit: scannedUnit || '',
                candidates: candidates,
                mode: 'find',
              };
              return;
            }
          }
        } catch {}
      }
      this.openAddItem();
      Object.assign(this.itemForm, prefill);
    },

    parseOffQuantity(product) {
      const offUnitMap = {
        'g': 'g', 'gram': 'g', 'grams': 'g',
        'kg': 'kg', 'kilogram': 'kg', 'kilograms': 'kg',
        'oz': 'oz', 'ounce': 'oz', 'ounces': 'oz',
        'lb': 'lb', 'lbs': 'lb', 'pound': 'lb', 'pounds': 'lb',
        'ml': 'ml', 'milliliter': 'ml', 'milliliters': 'ml', 'millilitre': 'ml',
        'l': 'L', 'L': 'L', 'liter': 'L', 'liters': 'L', 'litre': 'L', 'litres': 'L',
        'cl': 'ml',
      };
      let qty = null, unit = null;
      if (product.product_quantity && product.product_quantity_unit) {
        const rawUnit = product.product_quantity_unit.trim().toLowerCase();
        const mapped = offUnitMap[rawUnit] || offUnitMap[product.product_quantity_unit.trim()];
        if (mapped) {
          qty = parseFloat(product.product_quantity);
          unit = mapped;
          if (rawUnit === 'cl') qty = qty * 10;
        }
      }
      if ((qty === null || unit === null) && product.quantity) {
        const m = product.quantity.trim().match(/^([\d.,]+)\s*([a-zA-Z]+)/);
        if (m) {
          const rawUnit = m[2].trim().toLowerCase();
          const mapped = offUnitMap[rawUnit] || offUnitMap[m[2].trim()];
          if (mapped) {
            qty = parseFloat(m[1].replace(',', '.'));
            unit = mapped;
            if (rawUnit === 'cl') qty = qty * 10;
          }
        }
      }
      return (qty !== null && unit !== null && !isNaN(qty)) ? { qty, unit } : null;
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

    async openGroceryScanner() {
      if (this._scannerActive) this.closeScanner();
      this.groceryScanOpen = true;
      this.groceryScanPaused = false;
      this.groceryScanReview = false;
      this.groceryPendingItems = [];
      this._groceryProcessing = false;
      this._scanCooldown = false;
      this.groceryNewItem = null;
      this.groceryCameraError = '';
      await this.$nextTick();
      await this._startGroceryCamera();
    },

    async _startGroceryCamera() {
      if (!this.groceryScanOpen) return;
      const video = document.getElementById('grocery-scanner-video');
      try {
        const stream = await navigator.mediaDevices.getUserMedia({
          video: { facingMode: { ideal: 'environment' }, width: { ideal: 1920 }, height: { ideal: 1080 } }
        });
        if (!this.groceryScanOpen) { stream.getTracks().forEach(t => t.stop()); return; }
        this._stream = stream;
        video.srcObject = stream;
        await new Promise(resolve => { video.onloadedmetadata = resolve; });
        video.play();
      } catch {
        this.groceryCameraError = 'Camera access denied. Please allow camera access and try again.';
        return;
      }

      this._scannerActive = true;
      let lastScanErrorToast = 0;

      if (typeof BarcodeDetector !== 'undefined') {
        const detector = new BarcodeDetector({ formats: ['ean_13','ean_8','upc_a','upc_e','code_128','code_39','qr_code','itf','codabar'] });
        const scan = async () => {
          if (!this._scannerActive || this.groceryScanPaused) {
            if (this._scannerActive) this._scanRaf = requestAnimationFrame(scan);
            return;
          }
          try {
            const codes = await detector.detect(video);
            if (codes.length > 0) { lastScanErrorToast = 0; this.onGroceryBarcodeScanned(codes[0].rawValue); }
          } catch {
            const now = Date.now();
            if (now - lastScanErrorToast > 2000) {
              lastScanErrorToast = now;
              this.showToast("Couldn't read barcode", 'error');
            }
          }
          this._scanRaf = requestAnimationFrame(scan);
        };
        this._scanRaf = requestAnimationFrame(scan);
      } else {
        this._codeReader = new ZXing.BrowserMultiFormatReader();
        this._codeReader.decodeFromVideoElementContinuously(video, (result, err) => {
          if (result && !this.groceryScanPaused) { lastScanErrorToast = 0; this.onGroceryBarcodeScanned(result.getText()); return; }
          if (err && !(err instanceof ZXing.NotFoundException)) {
            const now = Date.now();
            if (now - lastScanErrorToast > 2000) {
              lastScanErrorToast = now;
              this.showToast("Couldn't read barcode", 'error');
            }
          }
        });
      }
    },

    closeGroceryScanner() {
      this.groceryScanOpen = false;
      this.groceryScanPaused = false;
      this.groceryScanReview = false;
      this.groceryPendingItems = [];
      this._scanCooldown = false;
      this.groceryNewItem = null;
      this._scannerActive = false;
      if (this._scanRaf) { cancelAnimationFrame(this._scanRaf); this._scanRaf = null; }
      if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
      if (this._stream) { this._stream.getTracks().forEach(t => t.stop()); this._stream = null; }
    },

    finishGroceryScan() {
      if (this.groceryPendingItems.length === 0) {
        this.closeGroceryScanner();
        return;
      }
      this._scannerActive = false;
      if (this._scanRaf) { cancelAnimationFrame(this._scanRaf); this._scanRaf = null; }
      if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
      if (this._stream) { this._stream.getTracks().forEach(t => t.stop()); this._stream = null; }
      this.groceryScanPaused = false;
      this.groceryNewItem = null;
      this.groceryScanReview = true;
    },

    async commitGroceryItems() {
      let successCount = 0, failed = 0;
      for (const pending of this.groceryPendingItems) {
        try {
          const cur = await fetch(this.baseUrl + '/api/inventory/barcode/' + encodeURIComponent(pending.barcode));
          if (!cur.ok) { failed++; continue; }
          const item = await cur.json();
          const newQty = parseFloat((item.quantity + pending.qty).toFixed(4));
          // A product with no lots left (id 0) gets a new lot; otherwise top up the lot.
          const patch = item.id === 0
            ? await this.apiFetch('/api/inventory/', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: item.name, quantity: pending.qty, unit: item.unit, preferred_unit: item.preferred_unit || '', barcode: pending.barcode }),
              }, { onError: () => {} })
            : await this.apiFetch('/api/inventory/' + item.id, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ quantity: newQty }),
              }, { onError: () => {} });
          if (patch) successCount++; else failed++;
        } catch { failed++; }
      }
      this.fetchInventory();
      this.closeGroceryScanner();
      if (failed > 0) this.showToast(`Updated ${successCount} item(s), ${failed} failed`, 'error');
      else this.showToast(`Updated ${successCount} item(s)`);
    },

    async onGroceryBarcodeScanned(code) {
      if (!this.groceryScanOpen || this.groceryScanPaused || this.groceryScanReview || this._groceryProcessing || this._scanCooldown) return;
      this.scannedCode = code;
      this._groceryProcessing = true;
      this._scanCooldown = true;
      setTimeout(() => { this._scanCooldown = false; }, 2500);

      try {
        const res = await fetch(this.baseUrl + '/api/inventory/barcode/' + encodeURIComponent(code));
        if (res.ok) {
          const item = await res.json();
          // Add to or update pending list
          const scanQty = item.quantity_per_scan > 0 ? item.quantity_per_scan : 1;
          const existing = this.groceryPendingItems.find(p => p.barcode === code);
          if (existing) {
            existing.qty = parseFloat((existing.qty + scanQty).toFixed(4));
          } else {
            this.groceryPendingItems.push({ id: item.id, name: item.name, qty: scanQty, unit: item.unit, barcode: code });
          }
          const entry = this.groceryPendingItems.find(p => p.barcode === code);
          this.showToast(`${item.name} +${scanQty} ${item.unit}`, 'success', `${entry.qty} ${item.unit}`);
          this._groceryProcessing = false;

          // OFf refinement in background (non-blocking)
          const qtyAtScanTime = entry.qty;
          (async () => {
            try {
              const offPromise = fetch('https://world.openfoodfacts.org/api/v2/product/' + encodeURIComponent(code) + '.json').then(r => r.json());
              const offData = await Promise.race([offPromise, new Promise((_, r) => setTimeout(() => r(new Error('timeout')), 3000))]);
              if (offData && offData.status === 1 && offData.product) {
                const parsed = this.parseOffQuantity(offData.product);
                if (parsed) {
                  const targetUnit = item.preferred_unit || item.unit;
                  const converted = this.convertUnits(parsed.qty, parsed.unit, targetUnit);
                  if (converted !== null && converted !== 1) {
                    const pendingEntry = this.groceryPendingItems.find(p => p.barcode === code);
                    if (pendingEntry && pendingEntry.qty === qtyAtScanTime) {
                      pendingEntry.qty = parseFloat(converted.toFixed(4));
                      pendingEntry.unit = targetUnit;
                    }
                  }
                }
              }
            } catch { /* silent */ }
          })();
          return;
        }
      } catch { /* fall through to new item flow */ }

      // Not in inventory — pause for unknown item form
      this._groceryProcessing = false;
      this.groceryScanPaused = true;
      let prefill = { barcode: code, quantity: 1, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 0 };

      try {
        const offPromise = fetch(`https://world.openfoodfacts.org/api/v2/product/${code}.json`).then(r => r.json());
        const offTimeout = new Promise((_, reject) => setTimeout(() => reject(new Error('timeout')), 3000));
        let offData = null;
        try { offData = await Promise.race([offPromise, offTimeout]); } catch { /* silent fallback */ }
        if (offData && offData.status === 1 && offData.product) {
          const p = offData.product;
          if (p.product_name) prefill.name = p.product_name;
          const parsed = this.parseOffQuantity(p);
          if (parsed) {
            prefill.quantity = parsed.qty;
            prefill.unit = parsed.unit;
            prefill.preferred_unit = parsed.unit;
          }
        }
      } catch { /* silent */ }

      // Location pre-fill from existing inventory
      if (prefill.name) {
        try {
          const invRes = await fetch(this.baseUrl + '/api/inventory/?name=' + encodeURIComponent(prefill.name));
          if (invRes.ok) {
            const invData = await invRes.json();
            if (invData && invData.length > 0 && invData[0].location) prefill.location = invData[0].location;
          }
        } catch { /* silent */ }
      }

      // Try to find merge candidates via similar search if we have a name
      if (prefill.name && prefill.name.trim()) {
        try {
          const similarRes = await fetch(this.baseUrl + '/api/inventory/similar?name=' + encodeURIComponent(prefill.name));
          if (similarRes.ok) {
            const candidates = await similarRes.json();
            if (candidates && candidates.length > 0) {
              // Show merge dialog
              this.skuMergeDialog = {
                barcode: code,
                name: prefill.name,
                quantity: prefill.quantity || 1,
                unit: prefill.unit || '',
                candidates: candidates,
                mode: 'grocery',
              };
              this.groceryScanPaused = false;
              return;
            }
          }
        } catch {}
      }

      this.groceryNewItem = prefill;
      this.groceryItemForm = { name: prefill.name || '', quantity: prefill.quantity, unit: prefill.unit, preferred_unit: prefill.preferred_unit, location: prefill.location, expiration_date: '', low_threshold: 0, barcode: code, quantity_per_scan: prefill.quantity || 1 };
    },

    async confirmGroceryNewItem() {
      const res = await this.apiFetch('/api/inventory/', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(this.groceryItemForm),
      }, { errorPrefix: 'Failed to add item' });
      if (res) {
        this.showToast(`Added ${this.groceryItemForm.name || 'item'} to inventory`);
        this.fetchInventory(); // background refresh, no await
      }
      this.skipGroceryNewItem();
    },

    skipGroceryNewItem() {
      this.groceryNewItem = null;
      this.groceryScanPaused = false;
      this.groceryItemForm = { name: '', quantity: 1, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 0, barcode: '' };
    },

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
      this.itemForm = { name: '', quantity: 0, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 0, barcode: '', quantity_per_scan: 1 };
      this.suggestions = [];
      this.showSuggestions = false;
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
      this.itemForm = { ...locOrItem, preferred_unit: locOrItem.preferred_unit || '' };
      this.modal = 'item';
    },

    openDeductModal(group) {
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
      this.showToast(`${group.name}: deducted ${qty} ${group.unit}`);
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

    async deleteItem(id) {
      if (!confirm('Delete this item?')) return;
      const res = await this.apiFetch('/api/inventory/' + id, { method: 'DELETE', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: 'Delete failed' });
      if (!res) return;
      await this.fetchInventory();
      this.showToast('Item deleted');
    },

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

    // --- Recipes ---
    async fetchRecipes() {
      const params = new URLSearchParams();
      if (this.recipeTagFilter) params.set('tag', this.recipeTagFilter);
      if (this.recipeAvailableOnly) params.set('available_only', 'true');
      params.set('include_archived', 'true'); // calendar needs archived names; list filters client-side
      const res = await fetch(this.baseUrl + '/api/recipes/?' + params);
      this.recipes = await res.json();
    },

    openAddRecipe() {
      this.recipeForm = { id: null, name: '', description: '', instructions: '', tags: '', servings: this.defaultServings(), ingredients: [] };
      this.modal = 'recipe';
    },

    editRecipe(recipe) {
      this.recipeForm = {
        id: recipe.id,
        name: recipe.name,
        description: recipe.description || '',
        instructions: recipe.instructions || '',
        tags: recipe.tags || '',
        servings: recipe.servings || this.defaultServings(),
        ingredients: (recipe.ingredients || []).map(i => ({
          product_id: i.product_id || null,
          name: i.name,
          quantity: i.quantity,
          unit: i.unit || '',
        })),
      };
      this.modal = 'recipe';
    },

    openImportRecipe() {
      this.recipeImportTab = 'url';
      this.recipeImportUrl = '';
      this.recipeImportFile = null;
      this.recipeImportText = '';
      this.recipeImporting = false;
      this.recipeImportError = '';
      this.modal = 'recipeImport';
    },

    async runRecipeImport() {
      this.recipeImportError = '';
      if (this.recipeImportTab === 'paste') {
        const text = this.recipeImportText.trim();
        if (!text) return;
        this.recipeForm = { id: null, name: '', description: '', instructions: text, tags: '', servings: this.defaultServings(), ingredients: [] };
        this.modal = 'recipe';
        return;
      }
      if (this.recipeImportTab === 'url') {
        if (!this.recipeImportUrl) return;
        this.recipeImporting = true;
        try {
          const res = await this.apiFetch('/api/recipes/import/url', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ url: this.recipeImportUrl }),
          }, { onError: m => { this.recipeImportError = m + ' — try the Paste tab instead.'; } });
          if (!res) return;
          const data = await res.json();
          this._applyImportData(data);
        } catch (e) {
          this.recipeImportError = 'Network error — try the Paste tab instead.';
        } finally {
          this.recipeImporting = false;
        }
        return;
      }
      if (this.recipeImportTab === 'pdf') {
        if (!this.recipeImportFile) return;
        this.recipeImporting = true;
        try {
          const fd = new FormData();
          fd.append('file', this.recipeImportFile);
          const res = await this.apiFetch('/api/recipes/import/pdf', { method: 'POST', body: fd }, { onError: m => { this.recipeImportError = m; } });
          if (!res) return;
          const data = await res.json();
          this._applyImportData(data);
        } catch (e) {
          this.recipeImportError = 'Network error';
        } finally {
          this.recipeImporting = false;
        }
      }
    },

    _applyImportData(data) {
      if (data.recipe) {
        const rec = data.recipe;
        const ingredients = (rec.ingredients || []).map(i => ({ name: i.name, quantity: i.quantity, unit: i.unit, product_id: null }));
        this.recipeForm = {
          id: null,
          name: rec.name || '',
          description: rec.description || '',
          instructions: rec.instructions || '',
          tags: rec.tags || '',
          servings: rec.servings || this.defaultServings(),
          ingredients: [],
        };
        this.modal = 'recipe';
        this.$nextTick(() => { this.recipeForm.ingredients = ingredients; });
      } else {
        this.recipeForm = { id: null, name: '', description: '', instructions: data.text || '', tags: '', servings: this.defaultServings(), ingredients: [] };
        this.modal = 'recipe';
      }
    },

    async saveRecipe() {
      if (!this.recipeForm.name) return;
      const isEdit = !!this.recipeForm.id;
      const res = await this.apiFetch(isEdit ? '/api/recipes/' + this.recipeForm.id : '/api/recipes/', {
        method: isEdit ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(this.recipeForm),
      });
      if (!res) return;
      this.modal = null;
      await this.fetchRecipes();
      this.showToast(isEdit ? 'Recipe updated' : 'Recipe saved');
    },

    viewRecipe(recipe) {
      this.selectedRecipe = recipe;
      this.recipeViewDate = new Date().toISOString().split('T')[0];
      this.recipeViewSlot = 'dinner';
      this.modal = 'viewRecipe';
    },

    async fetchSettings() {
      try {
        const res = await fetch(this.baseUrl + '/api/settings');
        if (res.ok) this.settings = await res.json();
      } catch (e) { /* non-fatal */ }
    },

    defaultServings() {
      const n = parseInt(this.settings && this.settings.default_servings);
      return n > 0 ? n : 2;
    },

    // Denominator for scaling; mirrors units.ServingsScale (0 -> 1).
    recipeBaseServings(recipe) {
      return recipe && recipe.servings > 0 ? recipe.servings : 1;
    },

    recipeScaleServings(recipe) {
      const v = this.recipeServings[recipe.id];
      if (v > 0) return v;
      return recipe.servings > 0 ? recipe.servings : this.defaultServings();
    },

    setRecipeServings(recipe, v) {
      v = Math.max(1, parseInt(v) || 1);
      this.recipeServings = { ...this.recipeServings, [recipe.id]: v };
    },

    scaledQty(qty, servings, recipe) {
      return qty * servings / this.recipeBaseServings(recipe);
    },

    formatQty(n) {
      return parseFloat(Number(n).toFixed(2)).toString();
    },

    onCalendarRecipeChange() {
      const r = this.recipes.find(r => r.id === this.calendarEntryForm.recipe_id);
      this.calendarEntryForm.servings = r ? this.recipeScaleServings(r) : null;
    },

    async addRecipeToCalendar(recipe, servings) {
      if (!this.recipeViewDate) return;
      const res = await this.apiFetch('/api/calendar/', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ date: this.recipeViewDate, meal_slot: this.recipeViewSlot, recipe_id: recipe.id, servings }),
      });
      if (!res) return;
      this.modal = null;
      await this.fetchCalendar();
      this.showToast(`Added to calendar: ${this.recipeViewDate}`);
    },

    async addRecipeToShoppingList(recipe, servings) {
      const s = servings || this.recipeScaleServings(recipe);
      const res = await this.apiFetch('/api/recipes/' + recipe.id + '/add-to-shopping-list?servings=' + s, { method: 'POST', headers: { 'Content-Type': 'application/json' } });
      if (!res) return;
      const data = await res.json();
      this.modal = null;
      this.showToast(`Added ${data.added} new, updated ${data.merged || 0} item(s) on shopping list`);
      await this.fetchShoppingList();
    },

    cookIt(recipe, servings, calendarEntryId = null) {
      const s = servings || this.recipeScaleServings(recipe);
      this.cookItConfirm = { recipe, servings: s, calendarEntryId };
      this.modal = 'cookItConfirm';
    },

    async confirmCookIt() {
      const { recipe, servings, calendarEntryId } = this.cookItConfirm;
      const payload = { recipe_id: recipe.id, servings_made: servings };
      if (calendarEntryId) payload.calendar_entry_id = calendarEntryId;
      const res = await this.apiFetch('/api/meals', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      }, { errorPrefix: 'Failed to log meal' });
      if (!res) return;
      this.modal = null;
      this.cookItConfirm = null;
      this.showToast(`Cooked ${recipe.name} (${servings} serving${servings !== 1 ? 's' : ''})`, 'success');
      try { const b = await res.json(); (b.warnings || []).forEach(w => this.showToast(w, 'error')); } catch (e) {}
      await this.fetchInventory();
      await this.fetchExpiring();
      if (calendarEntryId) await this.fetchCalendar();
    },

    async setRecipeArchived(id, archive) {
      const res = await this.apiFetch('/api/recipes/' + id + (archive ? '/archive' : '/unarchive'), { method: 'POST', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: `Failed to ${archive ? 'archive' : 'restore'}` });
      if (!res) return;
      await this.fetchRecipes();
      this.showToast(archive ? 'Recipe archived' : 'Recipe restored');
    },
    archiveRecipe(id) { return this.setRecipeArchived(id, true); },
    unarchiveRecipe(id) { return this.setRecipeArchived(id, false); },

    async deleteRecipe(id) {
      let msg = 'Permanently delete this recipe? Cooking history will be kept. (Tip: Archive hides it without deleting.)';
      try {
        const res = await fetch(this.baseUrl + '/api/recipes/' + id + '/future-calendar');
        if (res.ok) {
          const { count } = await res.json();
          if (count > 0) msg = `This recipe is scheduled ${count} time${count !== 1 ? 's' : ''} on the calendar; those entries will be removed. Permanently delete anyway? Cooking history will be kept.`;
        }
      } catch (e) { /* fall through to plain confirm */ }
      if (!confirm(msg)) return;
      const delRes = await this.apiFetch('/api/recipes/' + id, { method: 'DELETE', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: 'Failed to delete' });
      if (!delRes) return;
      await this.fetchRecipes();
      this.showToast('Recipe deleted');
    },

    // --- Calendar ---
    getMonday(d) {
      const day = d.getDay();
      const diff = d.getDate() - day + (day === 0 ? -6 : 1);
      const monday = new Date(d.setDate(diff));
      monday.setHours(0, 0, 0, 0);
      return monday;
    },

    get visibleRecipes() {
      return this.recipes.filter(r => this.recipeShowArchived || !r.archived);
    },
    get activeRecipes() {
      return this.recipes.filter(r => !r.archived);
    },
    get calendarWeekLabel() {
      if (!this.calendarWeekStart) return '';
      const end = new Date(this.calendarWeekStart);
      end.setDate(end.getDate() + 6);
      return this.calendarWeekStart.toLocaleDateString('en-US', { month: 'short', day: 'numeric' }) +
        ' – ' + end.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
    },

    get calendarDays() {
      if (!this.calendarWeekStart) return [];
      const days = [];
      for (let i = 0; i < 7; i++) {
        const d = new Date(this.calendarWeekStart);
        d.setDate(d.getDate() + i);
        days.push({
          date: d.toISOString().split('T')[0],
          label: d.toLocaleDateString('en-US', { weekday: 'long', month: 'short', day: 'numeric' }),
        });
      }
      return days;
    },

    prevWeek() {
      const d = new Date(this.calendarWeekStart);
      d.setDate(d.getDate() - 7);
      this.calendarWeekStart = d;
      this.fetchCalendar();
    },

    nextWeek() {
      const d = new Date(this.calendarWeekStart);
      d.setDate(d.getDate() + 7);
      this.calendarWeekStart = d;
      this.fetchCalendar();
    },

    async fetchCalendar() {
      const start = this.calendarWeekStart.toISOString().split('T')[0];
      const res = await fetch(this.baseUrl + '/api/calendar/week?start=' + start);
      this.calendarEntries = await res.json();
    },

    getCalendarEntries(date, slot) {
      return this.calendarEntries.filter(e => e.date === date && e.meal_slot === slot);
    },

    getOtherCalendarEntries(date) {
      return this.calendarEntries.filter(e => e.date === date && !this.mealSlots.includes(e.meal_slot));
    },

    getRecipeName(recipeId) {
      const r = this.recipes.find(r => r.id === recipeId);
      return r ? r.name : 'Unknown';
    },

    openAddCalendarEntry(date, slot) {
      this.calendarEntryForm = { id: null, date, meal_slot: slot, recipe_id: '', servings: null };
      this.modal = 'calendarEntry';
    },

    openEditCalendarEntry(entry) {
      this.calendarEntryForm = {
        id: entry.id, date: entry.date,
        meal_slot: this.mealSlots.includes(entry.meal_slot) ? entry.meal_slot : 'dinner',
        recipe_id: entry.recipe_id, servings: entry.servings,
      };
      this.modal = 'calendarEntry';
    },

    async saveCalendarEntry() {
      const f = this.calendarEntryForm;
      let res;
      if (f.id) {
        if (!f.date || !(f.servings > 0)) { this.showToast('Date and servings are required', 'error'); return; }
        res = await this.apiFetch('/api/calendar/' + f.id, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ date: f.date, meal_slot: f.meal_slot, servings: f.servings }),
        });
      } else {
        if (!f.recipe_id) return;
        if (!(f.servings > 0)) this.onCalendarRecipeChange();
        res = await this.apiFetch('/api/calendar/', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(f),
        });
      }
      if (!res) return;
      this.modal = null;
      await this.fetchCalendar();
      this.showToast(f.id ? 'Meal updated' : 'Meal added');
    },

    async deleteCalendarEntry(id) {
      const res = await this.apiFetch('/api/calendar/' + id, { method: 'DELETE', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: 'Remove failed' });
      if (!res) return;
      await this.fetchCalendar();
      this.showToast('Meal removed');
    },

    async generateWeeklyShopping() {
      const start = this.calendarWeekStart.toISOString().split('T')[0];
      const res = await this.apiFetch('/api/calendar/generate-weekly-shopping?start=' + start, { method: 'POST', headers: { 'Content-Type': 'application/json' } });
      if (!res) return;
      const data = await res.json();
      this.showToast(`Added ${data.added ?? data.items.length} new, updated ${data.merged ?? 0} item(s)`);
      await this.fetchShoppingList();
      this.activeTab = 'shopping';
    },

    // --- Remove Stock Scanner ---
    async openRemoveStockScanner() {
      this.removeStockScanOpen = true;
      this._removeStockActive = false;
      await this.$nextTick();
      const video = document.getElementById('remove-stock-video');
      try {
        const stream = await navigator.mediaDevices.getUserMedia({
          video: { facingMode: { ideal: 'environment' }, width: { ideal: 1920 }, height: { ideal: 1080 } }
        });
        if (!this.removeStockScanOpen) { stream.getTracks().forEach(t => t.stop()); return; }
        this._removeStockStream = stream;
        video.srcObject = stream;
        await video.play();
      } catch {
        this.removeStockScanOpen = false;
        this.showToast('Camera unavailable', 'error');
        return;
      }
      this._removeStockActive = true;
      let cooldown = false;
      if (typeof BarcodeDetector !== 'undefined') {
        const detector = new BarcodeDetector({ formats: ['ean_13','ean_8','upc_a','upc_e','code_128','code_39'] });
        const scan = async () => {
          if (!this._removeStockActive) return;
          if (!cooldown) {
            try {
              const codes = await detector.detect(video);
              if (codes.length > 0) {
                cooldown = true;
                await this.onRemoveStockScanned(codes[0].rawValue);
                setTimeout(() => { cooldown = false; }, 2000);
              }
            } catch {}
          }
          this._removeStockRaf = requestAnimationFrame(scan);
        };
        this._removeStockRaf = requestAnimationFrame(scan);
      } else {
        this._codeReader = new ZXing.BrowserMultiFormatReader();
        this._codeReader.decodeFromVideoElementContinuously(video, async (result) => {
          if (result && !cooldown) {
            cooldown = true;
            await this.onRemoveStockScanned(result.getText());
            setTimeout(() => { cooldown = false; }, 2000);
          }
        });
      }
    },

    closeRemoveStockScanner() {
      this._removeStockActive = false;
      if (this._removeStockRaf) { cancelAnimationFrame(this._removeStockRaf); this._removeStockRaf = null; }
      if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
      if (this._removeStockStream) { this._removeStockStream.getTracks().forEach(t => t.stop()); this._removeStockStream = null; }
      this.removeStockScanOpen = false;
    },

    async onRemoveStockScanned(code) {
      try {
        const res = await fetch(this.baseUrl + '/api/inventory/barcode/' + encodeURIComponent(code));
        if (!res.ok) { this.showToast('Item not found', 'error'); return; }
        const item = await res.json();
        if (item.id === 0) { this.showToast(`${item.name}: nothing in stock`, 'error'); return; }
        const scanQty = item.quantity_per_scan > 0 ? item.quantity_per_scan : 1;
        const patch = await this.apiFetch('/api/inventory/' + item.id + '/deduct?source=barcode_remove', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ quantity: scanQty }),
        }, { errorPrefix: 'Update failed' });
        if (patch) {
          this.showToast(`${item.name}: −${scanQty} ${item.preferred_unit || item.unit}`, 'success');
          this.fetchInventory();
        }
      } catch {
        this.showToast('Scan error', 'error');
      }
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

    // --- History ---
    async fetchHistory() {
      const res = await fetch(this.baseUrl + '/api/history?limit=50');
      if (res.ok) {
        const data = await res.json();
        this.historyItems = data.rows || [];
      }
    },

    // --- Meals ---
    async fetchMeals() {
      this.mealsOffset = 0;
      const res = await fetch(this.baseUrl + '/api/meals?limit=20&offset=0');
      if (res.ok) {
        const data = await res.json();
        this.meals = data.meals;
        this.mealsTotal = data.total;
      }
    },

    async loadMoreMeals() {
      this.mealsOffset += 20;
      const res = await fetch(this.baseUrl + '/api/meals?limit=20&offset=' + this.mealsOffset);
      if (res.ok) {
        const data = await res.json();
        this.meals = this.meals.concat(data.meals);
        this.mealsTotal = data.total;
      }
    },

    async fetchMealStats() {
      const tzOffset = -new Date().getTimezoneOffset();
      const res = await fetch(this.baseUrl + '/api/meals/stats?tz_offset=' + tzOffset + '&tz=' + encodeURIComponent(Intl.DateTimeFormat().resolvedOptions().timeZone || ''));
      if (res.ok) this.mealsStats = await res.json();
    },
  };
}
